package evaluaterun

import (
	"brain/internal/index"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
)

// PrepareComparison captures two equal real-binary indexes before any query.
// It uses the signed retrieval floor in normal mode and never creates a new claim.
func PrepareComparison(ctx context.Context, repo string, opt ComparisonOptions) (Baseline, error) {
	var baseline Baseline
	repoAbs, err := filepath.Abs(repo)
	if err != nil {
		return baseline, err
	}
	repo = systemAlias(repoAbs)
	opt = fillComparisonOptions(repo, opt)
	issue := opt.issueCommand
	if issue == nil {
		issue = func(d *driver, c context.Context, words ...string) commandRow { return d.command(c, words...) }
	}
	if opt.ConfigPath == "" || opt.StateRoot == "" {
		return baseline, errors.New("comparison needs config and new state root")
	}
	for _, path := range []string{opt.ConfigPath, opt.StateRoot, opt.BinaryPath, opt.KeywordPath, opt.SentencePath, opt.FloorPath} {
		if _, err := checkedPath(path, repo); err != nil {
			return baseline, err
		}
	}
	if _, err := os.Lstat(opt.StateRoot); err == nil {
		return baseline, errors.New("comparison state root already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return baseline, err
	}
	root, err := filepath.Abs(opt.CorpusRoot)
	if err != nil {
		return baseline, err
	}
	root = systemAlias(root)
	for _, protected := range protectedPaths(repo) {
		if containsPath(protected, root) {
			return baseline, errors.New("protected corpus root is forbidden")
		}
	}
	for step := root; ; step = filepath.Dir(step) {
		info, err := os.Lstat(step)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return baseline, errors.New("linked or missing corpus root")
		}
		if step == filepath.Dir(step) {
			break
		}
	}
	keywords, keywordHash, keywordPath, err := loadQuestions(opt.KeywordPath, repo, 30, true)
	if err != nil {
		return baseline, err
	}
	sentences, sentenceHash, sentencePath, err := loadQuestions(opt.SentencePath, repo, 20, false)
	if err != nil {
		return baseline, err
	}
	if keywords.Status != "frozen; unscored" || sentences.Status != "frozen; unscored" || keywords.ExactNameRule != sentences.ExactNameRule {
		return baseline, errors.New("question sets are not frozen")
	}
	config, rawConfig, configHash, configPath, err := loadConfig(opt.ConfigPath, repo)
	if err != nil {
		return baseline, err
	}
	for _, source := range config.Sources {
		if !containsPath(root, source.Path) || containsPath(source.Path, opt.StateRoot) || containsPath(opt.StateRoot, source.Path) {
			return baseline, errors.New("source and state boundary is invalid")
		}
	}
	minimum, err := checkFloor(opt.FloorPath, repo, keywordHash, sentenceHash)
	if err != nil {
		return baseline, err
	}
	keywordBytes, _, err := readControl(keywordPath, repo, 2<<20)
	if err != nil || digest(keywordBytes) != keywordHash {
		return baseline, errors.New("keyword input changed before capture")
	}
	sentenceBytes, _, err := readControl(sentencePath, repo, 2<<20)
	if err != nil || digest(sentenceBytes) != sentenceHash {
		return baseline, errors.New("sentence input changed before capture")
	}
	configBytes, _, err := readControl(configPath, repo, 2<<20)
	if err != nil || digest(configBytes) != configHash {
		return baseline, errors.New("config changed before capture")
	}
	floorBytes, _, err := readControl(opt.FloorPath, repo, 1<<20)
	if err != nil {
		return baseline, err
	}
	found, skipped, err := discoverManifest(config)
	if err != nil {
		return baseline, err
	}
	for _, control := range []string{configPath, keywordPath, sentencePath, opt.FloorPath} {
		if _, ok := found[control]; ok {
			return baseline, errors.New("comparison control file is eligible as a source")
		}
	}
	before := manifestFromFound(config, found)
	if _, err := ownerMap(keywords, root, found, before); err != nil {
		return baseline, err
	}
	if _, err := ownerMap(sentences, root, found, before); err != nil {
		return baseline, err
	}
	if err := os.Mkdir(opt.StateRoot, 0700); err != nil {
		return baseline, err
	}
	states := []string{filepath.Join(opt.StateRoot, "lexical"), filepath.Join(opt.StateRoot, "hybrid")}
	captures := make([]comparisonCapture, 2)
	inventories := make([][]index.InventoryChunk, 2)
	for i := range states {
		if i == 1 && opt.afterFirstCapture != nil {
			opt.afterFirstCapture()
		}
		captures[i], inventories[i], err = captureComparisonIndex(ctx, repo, opt.StateRoot, states[i], opt.BinaryPath, config, rawConfig, before, skipped, issue)
		if err != nil {
			return baseline, fmt.Errorf("capture %d: %w", i+1, err)
		}
	}
	first, second := captures[0], captures[1]
	if first.ManifestSHA256 != second.ManifestSHA256 || first.EffectiveConfigSHA != second.EffectiveConfigSHA || first.BinarySHA256 != second.BinarySHA256 || first.OutcomesSHA256 != second.OutcomesSHA256 || first.InventorySHA256 != second.InventorySHA256 || first.IndexChunks != second.IndexChunks || !reflect.DeepEqual(first.IndexGaps, second.IndexGaps) {
		return baseline, errors.New("comparison captures differ before scoring")
	}
	paths, hashes := make([]string, 2), make([]string, 2)
	for i := range captures {
		paths[i] = filepath.Join(opt.StateRoot, "capture-"+filepath.Base(states[i])+".json")
		if err := writeExclusive(paths[i], captures[i]); err != nil {
			return baseline, err
		}
		data, _, err := readControl(paths[i], repo, 512<<20)
		if err != nil {
			return baseline, err
		}
		hashes[i] = digest(data)
	}
	selfPath, err := os.Executable()
	if err != nil {
		return baseline, err
	}
	selfBytes, _, err := readControl(selfPath, repo, 64<<20)
	if err != nil {
		return baseline, err
	}
	baseline = Baseline{SchemaVersion: 1, Status: "captured", Repo: repo, CorpusRoot: root, LexicalState: states[0], HybridState: states[1], LexicalDatabaseSHA: first.DatabaseSHA256, HybridDatabaseSHA: second.DatabaseSHA256, InventorySHA256: first.InventorySHA256, ManifestSHA256: first.ManifestSHA256, OutcomesSHA256: first.OutcomesSHA256, KeywordSHA256: keywordHash, SentenceSHA256: sentenceHash, ConfigSHA256: configHash, KeywordSnapshot: keywordBytes, SentenceSnapshot: sentenceBytes, ConfigSnapshot: configBytes, FloorPath: opt.FloorPath, FloorSHA256: digest(floorBytes), FloorMinimum: minimum, KeywordHits: 0, KeywordFloorMet: false, KeywordRecordPath: paths[0], KeywordRecordSHA: hashes[0], SecondRecordPath: paths[1], SecondRecordSHA: hashes[1], BinarySHA256: first.BinarySHA256, AdapterSHA256: digest(selfBytes), IndexChunks: first.IndexChunks, Rows: nil, Inventory: inventories[0], HybridCopyValid: true}
	confirmFrozen := func() error {
		for i := range states {
			currentSHA, err := sealedDatabaseSHA(states[i], repo)
			if err != nil || currentSHA != captures[i].DatabaseSHA256 {
				return errors.New("index changed during scoring")
			}
		}
		after, afterSkipped, _, err := scanManifest(config)
		if err != nil || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(skipped, afterSkipped) {
			return errors.New("corpus changed during comparison")
		}
		for _, pair := range []struct{ path, hash string }{{keywordPath, keywordHash}, {sentencePath, sentenceHash}, {configPath, configHash}} {
			data, _, err := readControl(pair.path, repo, 2<<20)
			if err != nil || digest(data) != pair.hash {
				return errors.New("frozen input changed during comparison")
			}
		}
		currentFloor, _, err := readControl(opt.FloorPath, repo, 1<<20)
		if err != nil || digest(currentFloor) != digest(floorBytes) {
			return errors.New("frozen floor changed during comparison")
		}
		return nil
	}
	retain := func() error {
		path := filepath.Join(opt.StateRoot, "baseline.json")
		if err := writeExclusive(path, baseline); err != nil {
			return err
		}
		baseline.RecordPath = path
		data, _, err := readControl(path, repo, 512<<20)
		if err != nil {
			return err
		}
		baseline.RecordSHA256 = digest(data)
		return nil
	}
	if opt.beforeScoring != nil {
		opt.beforeScoring()
	}
	allRows := make([][]ComparisonRow, 2)
	for i := range captures {
		allRows[i], err = lexicalComparisonRows(ctx, captures[i], root, keywords, sentences, issue)
		if err != nil {
			if len(allRows[0]) == 50 && (i == 0 || len(allRows[1]) == 50) {
				hits := 0
				completeKeywords := true
				for _, row := range allRows[0][:30] {
					if row.Status != "complete" || row.Hit == nil {
						completeKeywords = false
						break
					}
					if *row.Hit {
						hits++
					}
				}
				if i == 1 {
					for _, row := range allRows[0][30:] {
						if row.Status != "complete" || row.Hit == nil {
							completeKeywords = false
							break
						}
					}
				}
				if completeKeywords {
					if checkErr := confirmFrozen(); checkErr != nil {
						return baseline, errors.Join(err, checkErr)
					}
					baseline.Rows = allRows[0]
					if i == 1 {
						baseline.SecondRows = allRows[1]
					}
					baseline.KeywordHits = hits
					baseline.KeywordFloorMet = hits >= minimum
					baseline.Status = "partial"
					baseline.Failure = err.Error()
					if i == 1 {
						baseline.Failure = "second capture: " + err.Error()
					}
					if saveErr := retain(); saveErr != nil {
						return baseline, errors.Join(err, saveErr)
					}
				}
			}
			return baseline, err
		}
	}
	if !sameRankRows(allRows[0], allRows[1]) {
		return baseline, errors.New("lexical ranks differ between sealed captures")
	}
	if err := confirmFrozen(); err != nil {
		return baseline, err
	}
	hits := 0
	for _, row := range allRows[0][:30] {
		if *row.Hit {
			hits++
		}
	}
	baseline.Rows = allRows[0]
	baseline.KeywordHits = hits
	baseline.KeywordFloorMet = hits >= minimum
	baseline.Status = "complete"
	if err := retain(); err != nil {
		return baseline, err
	}
	return baseline, nil
}

func lexicalComparisonRows(ctx context.Context, capture comparisonCapture, root string, keywords, sentences questionSet, issue func(*driver, context.Context, ...string) commandRow) ([]ComparisonRow, error) {
	rows := make([]ComparisonRow, 50)
	position := 0
	for setIndex, set := range []questionSet{keywords, sentences} {
		name := "keyword"
		if setIndex == 1 {
			name = "sentence"
		}
		for _, q := range set.Entries {
			queryText := q.Query
			if name == "sentence" {
				queryText = q.Question
			}
			rows[position] = ComparisonRow{Set: name, ID: q.ID, Query: queryText, Owners: append([]string{}, q.Owners...), ExactName: *q.ExactName, Status: "unrun"}
			position++
		}
	}
	position = 0
	home := filepath.Join(filepath.Dir(capture.State), "home-"+filepath.Base(capture.State))
	d := &driver{binary: capture.CapturedBinary, state: capture.State, home: home}
	known := map[string]bool{}
	for _, item := range capture.Outcomes {
		if item.Status == "current" || item.Status == "superseded" {
			known[filepath.Clean(filepath.Join(item.Source, filepath.FromSlash(item.Path)))] = true
		}
	}
	for setIndex, set := range []questionSet{keywords, sentences} {
		name := "keyword"
		if setIndex == 1 {
			name = "sentence"
		}
		for _, q := range set.Entries {
			queryText := q.Query
			if name == "sentence" {
				queryText = q.Question
			}
			command := issue(d, ctx, "search", queryText, "--limit", "100", "--no-refresh")
			rows[position].Status = "failed"
			rows[position].PublicCommand = command
			public, err := commandSearch(command)
			if err != nil {
				return rows, fmt.Errorf("%s %s: %w", name, q.ID, err)
			}
			full, _, err := completeRank(capture.State, queryText)
			if err != nil {
				return rows, err
			}
			if err := parity(public, full); err != nil {
				return rows, err
			}
			if err := validateCitations(full.Results, known); err != nil {
				return rows, err
			}
			hit, top := score(full.Results, q.Owners, root)
			ranks := make([]ComparisonRank, len(full.Results))
			for j, item := range full.Results {
				ranks[j] = ComparisonRank{ID: item.ID, Path: item.Path, AlsoIn: append([]string{}, item.AlsoIn...), Score: item.Score}
			}
			rows[position] = ComparisonRow{Set: name, ID: q.ID, Query: queryText, Owners: append([]string{}, q.Owners...), ExactName: *q.ExactName, Mode: full.Query.Mode, AsRun: full.Query.AsRun, Total: full.Total, Status: "complete", Hit: &hit, Ranks: ranks, Files: distinctFiles(full.Results, root), TopThree: top, PublicCommand: command}
			position++
		}
	}
	return rows, nil
}
