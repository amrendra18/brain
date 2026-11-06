package cli

import (
	"brain/internal/corpus"
	"brain/internal/index"
	"brain/internal/platform"
	"brain/internal/settings"
	"brain/internal/skill"
	"brain/internal/state"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func skillText() string {
	commands := []skill.Command{}
	for _, name := range []string{"search", "show", "projects", "recent", "note", "map"} {
		lines := strings.Split(topicHelp(name, "brain"), "\n")
		purpose := strings.TrimSpace(lines[1])
		for _, line := range lines {
			if example, ok := strings.CutPrefix(line, "  Example: brain "); ok {
				commands = append(commands, skill.Command{Name: name, Purpose: purpose, Example: example})
			}
		}
	}
	return skill.Text(commands)
}

func runIntegration(command string, arguments []string, output io.Writer) (code int) {
	var buffer bytes.Buffer
	c := &call{command: command, flags: map[string]string{}, output: &buffer, shell: defaultShell()}
	err := c.loadPrivacy(arguments)
	defer func() {
		if c.privacyOutput(output, &buffer) != 0 {
			code = 1
		}
		c.closePrivacy()
	}()
	if err != nil {
		if c.scopeErr != nil {
			return c.settingsFailure(c.scopeErr)
		}
		return c.privacyFailure(err)
	}
	if code := c.parse(arguments); code != -1 {
		return code
	}
	if platform.CheckPrivateState(c.state) == nil {
		if err := settings.CheckRead(c.state); err != nil {
			return c.settingsFailure(err)
		}
	}
	if err := c.beginMutation(); err != nil {
		return c.settingsFailure(err)
	}
	defer c.endMutation()
	if command == "setup" {
		return c.setup()
	}
	return c.brief()
}

func (c *call) setup() int {
	if len(c.words) > 1 {
		return c.usage("unexpected-argument", "Unexpected argument "+inputName(c.words[1])+". setup takes one harness name.")
	}
	harness := "all"
	if len(c.words) == 1 {
		harness = c.words[0]
	}
	harnesses := []string{harness}
	if harness == "all" {
		harnesses = []string{"claude", "codex", "copilot", "vscode"}
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return c.usage("invalid-value", "The user home folder is unavailable. Set HOME or USERPROFILE to an absolute folder.")
	}
	executable, _ := os.Executable()
	generated := []byte(skillText())
	items := []skill.Installation{}
	for _, name := range harnesses {
		item, ok := skill.Setup(name, runtime.GOOS, home, executable, c.state, c.config)
		if !ok {
			return c.failure("harness-unknown", "Unknown harness "+inputName(name)+". Use claude, codex, copilot, vscode or all.", 2, []string{"setup", "all"})
		}
		item.SkillCurrent = skill.CurrentSkill(home, item.SkillFile, generated)
		items = append(items, item)
	}
	if c.flags["--write"] == "true" {
		for i := range items {
			relative, err := filepath.Rel(home, items[i].SkillFile)
			if err != nil || platform.SafeStatePath(home, relative) != nil {
				return c.setupUnwritable()
			}
			root, err := os.OpenRoot(home)
			if err != nil {
				return c.setupUnwritable()
			}
			err = root.MkdirAll(filepath.Dir(relative), 0700)
			root.Close()
			if err != nil || platform.WriteState(home, relative, generated) != nil {
				return c.setupUnwritable()
			}
			items[i].Written = true
			items[i].SkillCurrent = true
		}
	}
	if c.flags["--format"] == "json" {
		return WriteJSON(c.output, struct {
			Count  int                  `json:"count"`
			Total  int                  `json:"total"`
			Empty  bool                 `json:"empty"`
			Setups []skill.Installation `json:"setups"`
		}{len(items), len(items), false, items})
	}
	for _, item := range items {
		if _, err = writeText(c.output, item.Text()+"\n"); err != nil {
			return 1
		}
	}
	return 0
}
func (c *call) setupUnwritable() int {
	return c.failure("state-unwritable", "The user skill folder cannot be written safely. Review setup guidance and copy SKILL.md into a writable user skill folder.", 1, []string{"help", "setup"})
}

func (c *call) brief() int {
	facts := skill.Facts{}
	recover := func(problem string, words ...string) {
		facts.Problem = problem
		facts.Next, _ = c.next(words...)
	}
	if remote, _ := platform.RemoteFolder(c.state); remote {
		recover("the state folder is on a network path or drive. Choose a local state folder.", c.localRecovery()...)
	} else if err := platform.CheckPrivateState(c.state); err != nil && !errors.Is(err, os.ErrNotExist) {
		recover("the state folder cannot be verified as private. Choose a private local folder or repair access permissions.", "help", "doctor")
	} else if config, err := corpus.LoadFile(c.config, c.state); err != nil {
		if errors.Is(err, corpus.ErrConfigMissing) {
			recover("the selected config is missing; init writes it.", "init")
		} else {
			recover("the selected config cannot be read or is invalid; repair it or restore a saved copy.", "help", "doctor")
		}
	} else if err := c.guardConfigPrivacy(config); err != nil {
		if errors.Is(err, index.ErrPrivacyRefresh) {
			recover("stored content needs an explicit privacy refresh", "index")
		} else if index.Locked(err) || errors.Is(err, platform.ErrLocked) {
			recover("another Brain process is using the index. Try the brief again.", "--brief")
		} else if index.Rebuildable(err) {
			recover("the index cannot be used; index --full rebuilds it.", "index", "--full")
		} else {
			recover("the index is unavailable without confirmed damage. Check permissions and storage access.", "help", "doctor")
		}
	} else {
		db, err := index.OpenStored(c.state)
		if err != nil {
			if errors.Is(err, platform.ErrStateUnsafe) {
				recover("the state folder cannot be verified as private. Repair access permissions.", "help", "doctor")
			} else if index.Locked(err) || err == platform.ErrLocked {
				recover("another Brain process is using the index. Try the brief again.", "--brief")
			} else if _, problem := os.Stat(filepath.Join(c.state, "index.sqlite")); os.IsNotExist(problem) {
				recover("the state folder has no index yet; index builds it.", "index")
			} else if !index.Rebuildable(err) {
				recover("the index is unavailable without confirmed damage. Check permissions and storage access.", "help", "doctor")
			} else {
				recover("the index cannot be used; index --full rebuilds it.", "index", "--full")
			}
		} else {
			defer db.Close()
			folder, _ := os.Getwd()
			if resolved, err := filepath.EvalSymlinks(folder); err == nil {
				folder = resolved
			}
			gaps, gapErr := index.StoredGaps(db.DB)
			files, chunks, last, project, err := state.Brief(db.DB, config, folder)
			complete := last != 0
			if err == nil && last == 0 {
				last, _, err = index.ReadTime(db.DB)
			}
			if err == nil {
				err = gapErr
			}
			if err != nil && !index.Rebuildable(err) {
				recover("the index is unavailable without confirmed damage. Check permissions and storage access.", "help", "doctor")
			} else if err != nil || last == 0 {
				recover("the index cannot be used; index --full rebuilds it.", "index", "--full")
			} else {
				facts.Gaps = gaps.Count
				facts.Files, facts.Chunks = files, chunks
				facts.IndexComplete = complete
				if last != 0 {
					facts.Age = max(0, int(float64(time.Now().UnixNano())/1e9-last))
				}
				facts.Name, facts.Purpose, facts.ProjectFiles = project.Name, project.Purpose, project.Files
				facts.Next, _ = c.next("search", "your words")
				facts.Doctor, _ = c.next("doctor")
				if info, err := os.Stat(c.state); err == nil && info.Mode().Perm()&0222 == 0 {
					facts.ReadOnly = true
				}
			}
		}
	}
	if c.flags["--format"] == "" || c.flags["--format"] == "text" {
		guidance := "request this brief with --format copilot for exact commands; their arguments contain control characters"
		if hasTextControls(facts.Next) {
			facts.Next = guidance
		}
		if hasTextControls(facts.Doctor) {
			facts.Doctor = guidance
		}
	}
	text := skill.Brief(facts)
	switch c.flags["--format"] {
	case "json":
		return WriteJSON(c.output, struct {
			Text          string `json:"text"`
			IndexAgeS     int    `json:"index_age_s"`
			IndexComplete bool   `json:"index_complete"`
			Ready         bool   `json:"ready"`
			Problem       string `json:"problem,omitempty"`
		}{text, facts.Age, facts.IndexComplete, facts.Problem == "", facts.Problem})
	case "copilot":
		return WriteJSON(c.output, struct {
			AdditionalContext string `json:"additionalContext"`
		}{text})
	case "vscode":
		type context struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		}
		return WriteJSON(c.output, struct {
			HookSpecificOutput context `json:"hookSpecificOutput"`
		}{context{"SessionStart", text}})
	default:
		if _, err := writeText(c.output, fmt.Sprint(text)); err != nil {
			return 1
		}
		return 0
	}
}
