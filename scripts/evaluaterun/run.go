package evaluaterun

import (
	"brain/internal/corpus"
	"brain/internal/index"
	"brain/internal/platform"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"
)

const usage = "usage: scripts/evaluate --config FILE --state-dir NEW_STATE --questions FILE --sentences FILE --floor FILE --corpus-root DIR [--binary FILE] [--measure]\n       scripts/evaluate --promote-floor RECORD --floor FILE\n       scripts/evaluate --recover-measurement CLAIM\n"

func Run(ctx context.Context, repo string, args []string, out io.Writer) int {
	absolute, err := filepath.Abs(repo)
	if err != nil {
		fmt.Fprintln(out, "invalid repository path:", err)
		return 2
	}
	repo = systemAlias(absolute)
	flags := flag.NewFlagSet("evaluate", flag.ContinueOnError)
	flags.SetOutput(out)
	configPath := flags.String("config", "", "product-format config")
	statePath := flags.String("state-dir", "", "new owned state")
	binaryPath := flags.String("binary", filepath.Join(repo, ".local", "bin", "brain"+func() string {
		if runtime.GOOS == "windows" {
			return ".exe"
		}
		return ""
	}()), "target binary")
	questionPath := flags.String("questions", "", "frozen keywords")
	sentencePath := flags.String("sentences", "", "frozen sentences")
	floorPath := flags.String("floor", "", "measured floor")
	corpusRoot := flags.String("corpus-root", "", "owner path base")
	measure := flags.Bool("measure", false, "first measurement")
	promotePath := flags.String("promote-floor", "", "completed result to promote")
	recoverPath := flags.String("recover-measurement", "", "claim to recover")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprint(out, usage)
		return 2
	}
	if *promotePath != "" || *recoverPath != "" {
		if *promotePath != "" && *recoverPath != "" || *measure || *configPath != "" || *statePath != "" {
			fmt.Fprint(out, usage)
			return 2
		}
		control := *recoverPath
		if *promotePath != "" {
			control = *promotePath
			if _, err := checkedPath(*floorPath, repo); err != nil {
				fmt.Fprintln(out, "invalid evaluation input:", err)
				return 2
			}
		}
		if _, err := checkedPath(control, repo); err != nil {
			fmt.Fprintln(out, "invalid evaluation input:", err)
			return 2
		}
		var err error
		if *promotePath != "" {
			err = promote(*promotePath, *floorPath, repo)
		} else {
			err = recoverClaim(*recoverPath, repo)
		}
		if err != nil {
			fmt.Fprintln(out, "evidence:", err)
			return 1
		}
		fmt.Fprintln(out, "evidence validated without queries")
		return 0
	}
	if *configPath == "" || *statePath == "" || *questionPath == "" || *sentencePath == "" || *floorPath == "" || *corpusRoot == "" {
		fmt.Fprint(out, usage)
		return 2
	}
	return runMeasurement(ctx, repo, *configPath, *statePath, *binaryPath, *questionPath, *sentencePath, *floorPath, *corpusRoot, *measure, out)
}
func runMeasurement(ctx context.Context, repo, configPath, statePath, binaryPath, questionPath, sentencePath, floorPath, corpusRoot string, measure bool, out io.Writer) int {
	invalid := func(err error) int { fmt.Fprintln(out, "invalid evaluation input:", err); return 2 }
	fail := func(err error) int { fmt.Fprintln(out, "evaluation failed:", err); return 1 }
	// Reject supplied protected and linked paths before opening controls or creating state.
	paths := []string{configPath, statePath, binaryPath, questionPath, sentencePath, floorPath}
	for i, path := range paths {
		checked, err := checkedPath(path, repo)
		if err != nil {
			return invalid(err)
		}
		paths[i] = checked
	}
	configPath, statePath, binaryPath, questionPath, sentencePath, floorPath = paths[0], paths[1], paths[2], paths[3], paths[4], paths[5]
	if _, err := os.Lstat(statePath); err == nil {
		return invalid(errors.New("state path already exists"))
	} else if !errors.Is(err, os.ErrNotExist) {
		return invalid(err)
	}
	root, err := filepath.Abs(corpusRoot)
	if err != nil {
		return invalid(err)
	}
	root = systemAlias(root)
	for _, protected := range protectedPaths(repo) {
		if containsPath(protected, root) {
			return invalid(errors.New("protected corpus root is forbidden"))
		}
	}
	for step := root; ; step = filepath.Dir(step) {
		info, statErr := os.Lstat(step)
		if statErr != nil {
			return invalid(statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return invalid(errors.New("linked corpus root is forbidden"))
		}
		if step == filepath.Dir(step) {
			break
		}
	}
	keywords, keywordHash, questionPath, err := loadQuestions(questionPath, repo, 30, true)
	if err != nil {
		return invalid(err)
	}
	sentences, sentenceHash, sentencePath, err := loadQuestions(sentencePath, repo, 20, false)
	if err != nil {
		return invalid(err)
	}
	if keywords.Status != "frozen; unscored" || sentences.Status != "frozen; unscored" || keywords.ExactNameRule != sentences.ExactNameRule {
		return invalid(errors.New("question sets must be frozen before scoring"))
	}
	config, rawConfig, configHash, configPath, err := loadConfig(configPath, repo)
	if err != nil {
		return invalid(err)
	}
	keywordSnapshot, _, err := readControl(questionPath, repo, 2<<20)
	if err != nil || digest(keywordSnapshot) != keywordHash {
		return invalid(errors.New("frozen keyword input changed before measurement"))
	}
	sentenceSnapshot, _, err := readControl(sentencePath, repo, 2<<20)
	if err != nil || digest(sentenceSnapshot) != sentenceHash {
		return invalid(errors.New("frozen sentence input changed before measurement"))
	}
	configSnapshot, _, err := readControl(configPath, repo, 2<<20)
	if err != nil || digest(configSnapshot) != configHash {
		return invalid(errors.New("evaluation config changed before measurement"))
	}
	for _, s := range config.Sources {
		if !containsPath(root, s.Path) {
			return invalid(errors.New("evaluation source is outside corpus root"))
		}
		if containsPath(s.Path, statePath) || containsPath(statePath, s.Path) {
			return invalid(errors.New("source and state overlap"))
		}
	}
	minimum := 0
	if !measure {
		minimum, err = checkFloor(floorPath, repo, keywordHash, sentenceHash)
		if err != nil {
			return invalid(err)
		}
	}
	found, skipped, err := discoverManifest(config)
	if err != nil {
		return fail(err)
	}
	for _, control := range []string{configPath, questionPath, sentencePath, floorPath} {
		if _, ok := found[control]; ok {
			return invalid(errors.New("evaluation control file is eligible as a source"))
		}
	}
	before := manifestFromFound(config, found)
	if _, err = ownerMap(keywords, root, found, before); err != nil {
		return invalid(err)
	}
	if _, err = ownerMap(sentences, root, found, before); err != nil {
		return invalid(err)
	}
	manifestBytes, err := json.Marshal(before)
	if err != nil {
		return fail(err)
	}
	manifestHash := digest(manifestBytes)
	selfPath, err := os.Executable()
	if err != nil {
		return fail(err)
	}
	selfPath, err = checkedPath(selfPath, repo)
	if err != nil {
		return invalid(err)
	}
	selfInfo, err := os.Stat(selfPath)
	if err != nil || selfInfo.Size() > 64<<20 {
		return fail(errors.New("evaluator executable identity is unavailable or oversized"))
	}
	selfBytes, err := platform.ReadSource(selfPath, 64<<20)
	if err != nil {
		return fail(err)
	}
	adapterSHA := digest(selfBytes)
	base := filepath.Join(repo, ".local", "evaluation")
	if _, err := checkedPath(base, repo); err != nil {
		return invalid(err)
	}
	if err = os.MkdirAll(base, 0700); err != nil {
		return fail(err)
	}
	attempt, err := os.MkdirTemp(base, time.Now().UTC().Format("20060102T150405Z")+"-")
	if err != nil {
		return fail(err)
	}
	resultPath := filepath.Join(attempt, "result.json")
	if err = writeExclusive(filepath.Join(attempt, "attempt-owner.json"), attemptOwner{KeywordSHA256: keywordHash}); err != nil {
		return fail(err)
	}
	claimPath := ""
	if measure {
		claimBase := filepath.Join(base, "claims")
		for _, path := range []string{claimBase, filepath.Join(claimBase, "locks", "measurement.lock"), filepath.Join(claimBase, keywordHash+".json")} {
			if _, err := checkedPath(path, repo); err != nil {
				return invalid(err)
			}
		}
		if err = os.MkdirAll(claimBase, 0700); err != nil {
			return fail(err)
		}
		lock, lockErr := platform.Acquire(claimBase, "measurement", true)
		if lockErr != nil {
			return fail(lockErr)
		}
		defer lock.Close()
		claimPath = filepath.Join(claimBase, keywordHash+".json")
		if data, _, readErr := readControl(claimPath, repo, 1<<20); readErr == nil {
			var previous claim
			if json.Unmarshal(data, &previous) != nil || previous.KeywordSHA256 != keywordHash {
				return fail(errors.New("ambiguous first measurement claim"))
			}
			if previous.Status != "invalid" {
				return fail(errors.New("first measurement already claimed"))
			}
			archived := claimPath + ".invalid-" + time.Now().UTC().Format("20060102T150405.000000000")
			if err = os.Rename(claimPath, archived); err != nil {
				return fail(err)
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return fail(readErr)
		}
		if err = writeExclusive(claimPath, claim{Status: "running", KeywordSHA256: keywordHash, ResultPath: resultPath}); err != nil {
			return fail(err)
		}
	}
	exclusions := append([]string{}, corpus.NeverRead...)
	exclusions = append(exclusions, config.Exclude...)
	for _, rule := range config.ThirdParty {
		included := false
		for _, allow := range config.Include {
			included = included || allow == rule
		}
		if !included {
			exclusions = append(exclusions, rule)
		}
	}
	record := resultRecord{Version: scoringVersion, Status: "invalid", CorpusRoot: root, KeywordSHA256: keywordHash, KeywordSnapshot: keywordSnapshot, SentenceSHA256: sentenceHash, SentenceSnapshot: sentenceSnapshot, ConfigSHA256: configHash, ConfigSnapshot: configSnapshot, AdapterSHA256: adapterSHA, ManifestSHA256: manifestHash, BinaryOriginal: binaryPath, Sources: config.Sources, EffectiveExclude: exclusions, Rows: make([]resultRow, len(keywords.Entries)), Files: before, Skipped: skipped}
	for i, q := range keywords.Entries {
		record.Rows[i] = resultRow{ID: q.ID, Query: q.Query, Status: "unrun"}
	}
	if revision, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output(); err == nil {
		record.Revision = strings.TrimSpace(string(revision))
	}
	saveFailure := func(err error) int {
		_ = replaceAtomic(resultPath, record)
		fmt.Fprintln(out, "evidence:", resultPath)
		return fail(err)
	}
	if err = os.Mkdir(statePath, 0700); err != nil {
		return saveFailure(err)
	}
	// Change only the fields the product normalizes, retaining future fields.
	var rawSources []map[string]json.RawMessage
	if err = json.Unmarshal(rawConfig["sources"], &rawSources); err != nil || len(rawSources) != len(config.Sources) {
		return saveFailure(errors.New("evaluation source objects are invalid"))
	}
	for i, source := range config.Sources {
		if rawSources[i] == nil {
			return saveFailure(errors.New("evaluation source object is invalid"))
		}
		rawSources[i]["path"], err = json.Marshal(source.Path)
		if err != nil {
			return saveFailure(err)
		}
		delete(rawSources[i], "kind")
	}
	rawConfig["sources"], err = json.Marshal(rawSources)
	if err != nil {
		return saveFailure(err)
	}
	data, err := json.MarshalIndent(rawConfig, "", "  ")
	if err != nil {
		return saveFailure(err)
	}
	if err = os.WriteFile(filepath.Join(statePath, "config.json"), append(data, '\n'), 0600); err != nil {
		return saveFailure(err)
	}
	record.EffectiveConfigSnapshot = append(append([]byte{}, data...), '\n')
	record.EffectiveConfigSHA256 = digest(record.EffectiveConfigSnapshot)
	home := filepath.Join(attempt, "home")
	if err = os.Mkdir(home, 0700); err != nil {
		return saveFailure(err)
	}
	scratch := filepath.Join(attempt, "scratch")
	if err = os.Mkdir(scratch, 0700); err != nil {
		return saveFailure(err)
	}
	captured, binaryHash, err := captureBinary(binaryPath, repo, scratch)
	if err != nil {
		return saveFailure(err)
	}
	record.BinarySHA256 = binaryHash
	record.CapturedBinary = captured
	d := &driver{binary: captured, state: statePath, home: home}
	indexCommand := d.command(ctx, "index")
	reportedChunks := 0
	indexSkipped, err := indexReply(indexCommand, &reportedChunks)
	if err != nil {
		record.Commands = d.commands
		return saveFailure(err)
	}
	if len(indexSkipped) > 0 {
		for _, gap := range indexSkipped {
			record.Skipped = append(record.Skipped, corpus.Skipped{Path: gap.Path, Reason: gap.Reason})
		}
	}
	outcomes, err := index.ReadFileOutcomes(statePath)
	if err != nil {
		record.Commands = d.commands
		return saveFailure(err)
	}
	record.Outcomes = outcomes
	byFile := map[string]index.FileOutcome{}
	manifestFiles := map[string]manifestRow{}
	for _, row := range before {
		manifestFiles[filepath.Join(row.Source, filepath.FromSlash(row.Path))] = row
	}
	for _, item := range outcomes {
		identity := filepath.Join(item.Source, filepath.FromSlash(item.Path))
		if _, ok := manifestFiles[identity]; !ok {
			record.Commands = d.commands
			return saveFailure(errors.New("index contains an unexpected active file"))
		}
		byFile[identity] = item
	}
	for _, row := range before {
		if row.Status != "eligible" {
			continue
		}
		item, ok := byFile[filepath.Join(row.Source, filepath.FromSlash(row.Path))]
		if !ok {
			record.Commands = d.commands
			return saveFailure(errors.New("eligible file missing from index outcome"))
		}
		if item.SHA256 != row.SHA256 {
			record.Commands = d.commands
			return saveFailure(errors.New("indexed raw digest differs from manifest"))
		}
	}
	_, chunks, _, err := func() (int, int, int, error) {
		db, openErr := index.OpenStored(statePath)
		if openErr != nil {
			return 0, 0, 0, openErr
		}
		defer db.Close()
		return index.Totals(db.DB)
	}()
	if err != nil {
		record.Commands = d.commands
		return saveFailure(err)
	}
	if chunks > 32765 {
		record.Commands = d.commands
		return saveFailure(errors.New("adapter-capacity-exceeded"))
	}
	if reportedChunks != chunks {
		record.Commands = d.commands
		return saveFailure(errors.New("index reply chunk count differs from stored index"))
	}
	record.IndexChunks = chunks
	for i, q := range keywords.Entries {
		row := d.command(ctx, "search", q.Query, "--limit", "100", "--no-refresh")
		reply, parseErr := commandSearch(row)
		if parseErr != nil {
			record.Rows[i].Status = "invalid"
			record.Rows[i].Error = parseErr.Error()
			record.Commands = d.commands
			return saveFailure(parseErr)
		}
		full, _, rankErr := completeRank(statePath, q.Query)
		if rankErr != nil {
			record.Rows[i].Status = "invalid"
			record.Commands = d.commands
			return saveFailure(rankErr)
		}
		if rankErr = parity(reply, full); rankErr != nil {
			record.Rows[i].Status = "invalid"
			record.Commands = d.commands
			return saveFailure(rankErr)
		}
		known := map[string]bool{}
		for identity, item := range byFile {
			if item.Status == "current" || item.Status == "superseded" {
				known[filepath.Clean(identity)] = true
			}
		}
		if err = validateCitations(full.Results, known); err != nil {
			record.Rows[i].Status = "invalid"
			record.Commands = d.commands
			return saveFailure(err)
		}
		hit, top := score(full.Results, q.Owners, root)
		record.Rows[i] = resultRow{ID: q.ID, Query: q.Query, QueryAsRun: full.Query.AsRun, QueryMode: full.Query.Mode, Total: full.Total, Status: "complete", Hit: &hit, TopThree: top, Ranked: compactRanks(full.Results), DurationMS: row.DurationMS}
		if hit {
			record.HitCount++
		}
	}
	record.Commands = d.commands
	after, afterSkipped, _, err := scanManifest(config)
	if err != nil {
		return saveFailure(err)
	}
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(skipped, afterSkipped) {
		return saveFailure(errors.New("corpus changed during measurement"))
	}
	for _, control := range []struct{ path, hash string }{{configPath, configHash}, {questionPath, keywordHash}, {sentencePath, sentenceHash}} {
		data, _, readErr := readControl(control.path, repo, 2<<20)
		if readErr != nil || digest(data) != control.hash {
			return saveFailure(errors.New("frozen input changed during measurement"))
		}
	}
	record.Status = "complete"
	if err = replaceAtomic(resultPath, record); err != nil {
		return fail(err)
	}
	if claimPath != "" {
		data, _, readErr := readControl(resultPath, repo, 512<<20)
		if readErr != nil {
			return fail(readErr)
		}
		if err = replaceAtomic(claimPath, claim{Status: "complete", KeywordSHA256: keywordHash, ResultPath: resultPath, ResultSHA256: digest(data)}); err != nil {
			return fail(err)
		}
	}
	fmt.Fprintf(out, "results: %d/30; evidence: %s\n", record.HitCount, resultPath)
	if measure {
		fmt.Fprintln(out, "first valid measurement awaits floor promotion")
		return 1
	}
	if record.HitCount < minimum {
		fmt.Fprintf(out, "floor: %d; measured: %d\n", minimum, record.HitCount)
		return 1
	}
	return 0
}
