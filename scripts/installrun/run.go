package installrun

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"brain/internal/corpus"
	"brain/internal/platform"
	"brain/internal/redact"
	"brain/internal/schedule"
	"brain/internal/settings"
	"brain/internal/skill"
	"brain/scripts/toolchain"
)

const Usage = "usage: scripts/install [--source DIR ...] [--skills codex,claude|none] [--state-dir DIR] [--config FILE] [--bin-dir DIR] [--time HH:MM] [--no-schedule] [--write]"

type sources []string

func (s *sources) String() string         { return strings.Join(*s, ",") }
func (s *sources) Set(value string) error { *s = append(*s, value); return nil }

type options struct {
	sources                        sources
	skills, state, config, bin, at string
	write, noSchedule              bool
	arguments                      []string
}

type selection struct {
	State  string `json:"state_dir"`
	Config string `json:"config"`
}

type dailyJob struct {
	Install bool     `json:"install"`
	Name    string   `json:"name,omitempty"`
	Time    string   `json:"time,omitempty"`
	Files   []string `json:"files,omitempty"`
}

type receipt struct {
	policy       *redact.Policy
	Status       string            `json:"status"`
	Binary       string            `json:"binary"`
	Selection    selection         `json:"selection"`
	SkillFiles   map[string]string `json:"skill_files"`
	BackupsDir   string            `json:"backups_dir,omitempty"`
	SettingsFile string            `json:"settings_file,omitempty"`
	DailyJob     dailyJob          `json:"daily_job"`
	Backup       string            `json:"backup,omitempty"`
	Completed    []string          `json:"completed"`
	Message      string            `json:"message,omitempty"`
}

type commandRunner func(string, ...string) ([]byte, error)

type installer struct {
	root, home, system string
	run                commandRunner
}

func Run(args []string, output io.Writer) int {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(output, "Cannot read the checkout folder.")
		return 1
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(output, "Cannot read the home folder.")
		return 1
	}
	return (&installer{root: root, home: home, system: runtime.GOOS, run: execute}).runInstall(args, output)
}

func execute(binary string, args ...string) ([]byte, error) {
	command := exec.Command(binary, args...)
	command.Env = append(os.Environ(), "CGO_ENABLED=0")
	data, err := command.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 3 {
		return data, nil
	}
	if err != nil {
		if errors.As(err, &exit) {
			return nil, fmt.Errorf("installation command %s did not complete (exit %d); retry that command directly for guidance", args[0], exit.ExitCode())
		}
		return nil, errors.New("an installation command could not start; check executable permissions")
	}
	return data, nil
}

func parse(args []string) (options, error) {
	o := options{arguments: args}
	flags := flag.NewFlagSet("install", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Var(&o.sources, "source", "initial source folder")
	flags.StringVar(&o.skills, "skills", "none", "codex,claude or none")
	flags.StringVar(&o.state, "state-dir", "", "state folder")
	flags.StringVar(&o.config, "config", "", "config file")
	flags.StringVar(&o.bin, "bin-dir", "", "binary folder")
	flags.StringVar(&o.at, "time", "", "daily local time")
	flags.BoolVar(&o.write, "write", false, "install")
	flags.BoolVar(&o.noSchedule, "no-schedule", false, "preserve schedule without registration")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return o, err
		}
		return o, errors.New("invalid installation arguments; check flag names and values")
	}
	if flags.NArg() != 0 {
		return o, errors.New("unexpected argument")
	}
	if o.at != "" {
		if _, err := time.Parse("15:04", o.at); err != nil || len(o.at) != 5 {
			return o, errors.New("--time needs HH:MM")
		}
	}
	seen := map[string]bool{}
	for _, name := range strings.Split(o.skills, ",") {
		if name != "codex" && name != "claude" && name != "none" || seen[name] || name == "none" && o.skills != "none" {
			return o, errors.New("--skills needs codex,claude or none")
		}
		seen[name] = true
	}
	for _, ptr := range []*string{&o.state, &o.config, &o.bin} {
		if *ptr != "" {
			absolute, err := filepath.Abs(*ptr)
			if err != nil {
				return o, err
			}
			*ptr = absolute
		}
	}
	for n, source := range o.sources {
		source, err := filepath.Abs(source)
		if err != nil {
			return o, err
		}
		source, err = filepath.EvalSymlinks(source)
		if err != nil {
			return o, errors.New("--source needs a readable folder")
		}
		info, err := os.Stat(source)
		if err != nil || !info.IsDir() || !platform.ReadableDir(source) {
			return o, errors.New("--source needs a readable folder")
		}
		o.sources[n] = source
	}
	return o, nil
}

func (i *installer) runInstall(args []string, output io.Writer) int {
	o, err := parse(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(output, Usage)
		return 0
	}
	if err != nil {
		fmt.Fprintln(output, safeError(err))
		fmt.Fprintln(output, Usage)
		return 2
	}
	result := receipt{policy: redact.Default(), Status: "preview", SkillFiles: map[string]string{}, Completed: []string{}}
	err = i.install(o, &result)
	if err != nil {
		result.Status = "partial"
		result.Message = safeError(err)
		if result.Backup != "" {
			_ = saveReceipt(result)
		}
	}
	if json.NewEncoder(output).Encode(result.safe()) != nil {
		return 1
	}
	if err != nil {
		return 1
	}
	return 0
}

func (i *installer) install(o options, result *receipt) error {
	if !filepath.IsAbs(i.home) {
		return errors.New("home must be absolute")
	}
	if o.bin == "" {
		o.bin = filepath.Join(i.home, ".local", "bin")
	}
	suffix := ""
	if i.system == "windows" {
		suffix = ".exe"
	}
	binary := filepath.Join(o.bin, "brain"+suffix)
	if err := safeTarget(binary); err != nil {
		return err
	}
	candidateDir, err := os.MkdirTemp(filepath.Join(i.root, ".local"), "install-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(candidateDir)
	candidate := filepath.Join(candidateDir, "brain"+suffix)
	goBinary := toolchain.Binary()
	if _, err = i.run(goBinary, "build", "-trimpath", "-mod=readonly", "-o", candidate, "./cmd/brain"); err != nil {
		return errors.New("installation build did not complete; check the pinned compiler and checkout")
	}
	if _, err = i.run(candidate, "--version", "--format", "json"); err != nil {
		return errors.New("candidate version could not be checked")
	}
	selected, err := i.selection(candidate, o.state, o.config)
	if err != nil {
		return err
	}

	configExists := false
	if _, err = os.Lstat(selected.Config); err == nil {
		configExists = true
		config, problem := corpus.LoadFile(selected.Config, selected.State)
		if problem != nil {
			return errors.New("existing config is invalid; repair it before installation")
		}
		result.policy = config.Privacy()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	backupRoot := filepath.Join(i.home, ".local", "share", "brain-install-backups")
	values := append([]string{i.root, i.home, binary, selected.State, selected.Config, backupRoot}, o.arguments...)
	values = append(values, o.sources...)
	if err = privateDestinations(result.policy, values...); err != nil {
		return err
	}
	result.Binary, result.Selection = binary, selected
	if configExists && len(o.sources) > 0 && !sameSources(result.Selection.Config, o.sources) {
		return errors.New("existing sources differ; omit --source to preserve them, or use sources add for changes")
	}
	if !configExists && len(o.sources) == 0 {
		return errors.New("first installation requires at least one explicit --source")
	}
	skills := []string{}
	if o.skills != "none" {
		skills = strings.Split(o.skills, ",")
	}
	for _, name := range skills {
		setup, ok := skill.Setup(name, i.system, i.home, result.Binary, result.Selection.State, result.Selection.Config)
		if !ok {
			return errors.New("unsupported platform")
		}
		if err = privateDestinations(result.policy, setup.SkillFile); err != nil {
			return err
		}
		if err = safeTarget(setup.SkillFile); err != nil {
			return err
		}
		result.SkillFiles[name] = setup.SkillFile
	}
	settingsRoot, err := settings.Root()
	if err != nil {
		return errors.New("settings folder could not be found")
	}
	result.BackupsDir, result.SettingsFile = backupRoot, filepath.Join(settingsRoot, "selection.json")
	if result.DailyJob, err = i.dailyJob(o, result); err != nil {
		return err
	}
	if err = privateDestinations(result.policy, append([]string{result.SettingsFile}, result.DailyJob.Files...)...); err != nil {
		return err
	}
	if !o.write {
		result.Message = "Preview only. Checkout build files may be created. Add --write to install."
		return nil
	}
	if inside(i.root, backupRoot) || inside(result.Selection.State, backupRoot) || inside(backupRoot, result.Selection.State) {
		return errors.New("backup folder must be outside the checkout and live state")
	}
	if err = safeDirectory(backupRoot, true); err != nil {
		return err
	}
	if err = platform.CheckPrivateState(backupRoot); err != nil {
		return err
	}
	lock, problem := platform.Acquire(backupRoot, "install.lock", false)
	if problem != nil {
		return errors.New("another installer is active, or the install lock is unsafe")
	}
	defer lock.Close()
	result.Backup, err = os.MkdirTemp(backupRoot, "install-")
	if err != nil {
		return err
	}
	result.Status = "partial"
	if err = saveReceipt(*result); err != nil {
		return err
	}
	if err = backupFile(result.Binary, filepath.Join(result.Backup, "brain.previous")); err != nil {
		return err
	}
	for _, name := range skills {
		if err = backupFile(result.SkillFiles[name], filepath.Join(result.Backup, name+".SKILL.md")); err != nil {
			return err
		}
	}
	if err = safeDirectory(o.bin, true); err != nil {
		return err
	}
	if err = platform.PrepareState(result.Selection.State); err != nil {
		return err
	}
	lease, problem := publicationLease(result.Selection.State)
	if problem != nil {
		return problem
	}
	err = settings.CheckMutation(result.Selection.State)
	if err == nil {
		current, problem := i.selection(candidate, result.Selection.State, result.Selection.Config)
		err = problem
		if err == nil && current != result.Selection {
			err = errors.New("selected state changed during installation")
		}
	}
	if err == nil {
		err = replaceBinary(candidate, result.Binary)
	}
	closeErr := lease.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = record(result, "binary"); err != nil {
		return err
	}
	call := func(step string, words ...string) error {
		words = append(words, "--state-dir", result.Selection.State, "--config", result.Selection.Config, "--format", "json")
		if _, problem := i.run(result.Binary, words...); problem != nil {
			return problem
		}
		return record(result, step)
	}
	if !configExists {
		words := []string{"init"}
		for _, source := range o.sources {
			words = append(words, "--source", source)
		}
		if err = call("init", words...); err != nil {
			return err
		}
	}
	if err = call("settings", "settings", "--write"); err != nil {
		return err
	}
	if err = call("index", "index"); err != nil {
		return err
	}
	for _, name := range skills {
		if err = call("skill-"+name, "setup", name, "--write"); err != nil {
			return err
		}
	}
	if !o.noSchedule {
		words := []string{"schedule", "--write"}
		if o.at != "" {
			words = append(words, "--time", o.at)
		}
		if err = call("schedule", words...); err != nil {
			return err
		}
	}
	result.Status = "installed"
	result.Message = "Installation complete. Add the binary folder to PATH if needed. Backups are retained."
	return saveReceipt(*result)
}

func (i *installer) dailyJob(o options, result *receipt) (dailyJob, error) {
	if o.noSchedule {
		return dailyJob{}, nil
	}
	state := canonical(result.Selection.State)
	at := o.at
	if at == "" {
		if err := platform.CheckPrivateState(state); err != nil && !errors.Is(err, os.ErrNotExist) {
			return dailyJob{}, err
		}
		saved, exists, err := schedule.Current(state)
		if err != nil {
			return dailyJob{}, errors.New("saved daily job could not be read; inspect it with brain schedule status")
		}
		at = "01:00"
		if exists {
			at = fmt.Sprintf("%02d:%02d", saved.Spec.Hour, saved.Spec.Minute)
		}
	}
	clock, err := time.Parse("15:04", at)
	if err != nil {
		return dailyJob{}, errors.New("--time needs HH:MM")
	}
	plan, err := schedule.Preview(i.system, schedule.Spec{Executable: canonical(result.Binary), Config: canonical(result.Selection.Config), State: state, Home: canonical(i.home), Hour: clock.Hour(), Minute: clock.Minute()})
	if err != nil {
		return dailyJob{}, errors.New("daily job could not be prepared on this platform; use --no-schedule")
	}
	job := dailyJob{Install: true, Name: plan.ID, Time: at}
	for _, artifact := range plan.Artifacts {
		job.Files = append(job.Files, artifact.Path)
	}
	return job, nil
}

func canonical(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	if parent, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		return filepath.Join(parent, filepath.Base(path))
	}
	return path
}

func record(result *receipt, step string) error {
	result.Completed = append(result.Completed, step)
	return saveReceipt(*result)
}
func saveReceipt(result receipt) error {
	data, err := json.MarshalIndent(result.safe(), "", "  ")
	if err != nil {
		return err
	}
	return platform.WriteState(result.Backup, "receipt.json", append(data, '\n'))
}
func inside(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}

func (i *installer) selection(binary, state, config string) (selection, error) {
	query := []string{"settings", "--format", "json"}
	if state != "" {
		query = append(query, "--state-dir", state)
	}
	if config != "" {
		query = append(query, "--config", config)
	}
	data, err := i.run(binary, query...)
	if err != nil {
		return selection{}, errors.New("candidate settings could not be read; inspect the selected state and config")
	}
	var reply struct {
		Selection           selection  `json:"selection"`
		SavedSelection      *selection `json:"saved_selection"`
		RegistrationAllowed bool       `json:"registration_allowed"`
	}
	if json.Unmarshal(data, &reply) != nil {
		return selection{}, errors.New("candidate returned invalid settings")
	}
	if !filepath.IsAbs(reply.Selection.State) || !filepath.IsAbs(reply.Selection.Config) {
		return selection{}, errors.New("candidate returned incomplete settings")
	}
	if !reply.RegistrationAllowed || reply.SavedSelection != nil && *reply.SavedSelection != reply.Selection {
		return selection{}, errors.New("saved state, config or job differs; inspect settings and use settings relocate before installing")
	}
	return reply.Selection, nil
}

func sameSources(config string, requested []string) bool {
	document, err := corpus.ReadDocument(config)
	if err != nil || len(document.Config.Sources) != len(requested) {
		return false
	}
	existing := make([]string, 0, len(requested))
	for _, source := range document.Config.Sources {
		if !source.Literal && strings.ContainsAny(source.Path, "*?[") {
			return false
		}
		path, err := filepath.EvalSymlinks(source.Path)
		if err != nil {
			return false
		}
		existing = append(existing, path)
	}
	expected := append([]string(nil), requested...)
	slices.Sort(existing)
	slices.Sort(expected)
	return slices.Equal(existing, expected)
}
