package evaluaterun

import (
	"brain/internal/corpus"
	"brain/internal/platform"
	"brain/internal/search"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
)

var errInvalidEvidence = errors.New("completed measurement evidence is invalid")

func invalidEvidence(reason string) error { return fmt.Errorf("%w: %s", errInvalidEvidence, reason) }

type claim struct {
	Status        string `json:"status"`
	KeywordSHA256 string `json:"keyword_sha256"`
	ResultPath    string `json:"result_path"`
	ResultSHA256  string `json:"result_sha256"`
}

type attemptOwner struct {
	KeywordSHA256 string `json:"keyword_sha256"`
}

func writeExclusive(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	return errors.Join(writeErr, syncErr, closeErr)
}
func replaceAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".record-")
	if err != nil {
		return err
	}
	if err = f.Chmod(0600); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err = errors.Join(writeErr, syncErr, closeErr); err != nil {
		os.Remove(f.Name())
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		os.Remove(f.Name())
	}
	return err
}
func readRecord(path, repo string) (resultRecord, string, error) {
	var record resultRecord
	data, checked, err := readControl(path, repo, 512<<20)
	if err != nil {
		return record, "", err
	}
	if err = json.Unmarshal(data, &record); err != nil {
		return record, "", fmt.Errorf("%w: %v", errInvalidEvidence, err)
	}
	if err = validateRecord(record, checked, repo); err != nil {
		return record, "", err
	}
	return record, digest(data), nil
}

func validateRecord(record resultRecord, path, repo string) error {
	if record.Version != scoringVersion || record.Status != "complete" || len(record.Rows) != 30 || record.HitCount < 0 || record.HitCount > 30 || record.CorpusRoot == "" {
		return invalidEvidence("status, scoring rule, question count, or corpus root")
	}
	for _, pair := range []struct {
		data []byte
		hash string
	}{{record.KeywordSnapshot, record.KeywordSHA256}, {record.SentenceSnapshot, record.SentenceSHA256}, {record.ConfigSnapshot, record.ConfigSHA256}, {record.EffectiveConfigSnapshot, record.EffectiveConfigSHA256}} {
		if len(pair.data) == 0 || len(pair.hash) != 64 || digest(pair.data) != pair.hash {
			return invalidEvidence("frozen input snapshot digest")
		}
	}
	var keywords, sentences questionSet
	if json.Unmarshal(record.KeywordSnapshot, &keywords) != nil || json.Unmarshal(record.SentenceSnapshot, &sentences) != nil || validateQuestionSet(keywords, 30, true) != nil || validateQuestionSet(sentences, 20, false) != nil || keywords.Status != "frozen; unscored" || sentences.Status != "frozen; unscored" || keywords.ExactNameRule != sentences.ExactNameRule {
		return invalidEvidence("frozen question snapshot")
	}
	var original, effective corpus.Config
	if json.Unmarshal(record.ConfigSnapshot, &original) != nil || json.Unmarshal(record.EffectiveConfigSnapshot, &effective) != nil || !reflect.DeepEqual(effective.Sources, record.Sources) || len(effective.Sources) == 0 || len(record.EffectiveExclude) == 0 {
		return invalidEvidence("config snapshot or effective sources")
	}
	manifestBytes, err := json.Marshal(record.Files)
	if err != nil || len(record.Files) == 0 || digest(manifestBytes) != record.ManifestSHA256 {
		return invalidEvidence("manifest digest")
	}
	if len(record.AdapterSHA256) != 64 || len(record.BinarySHA256) != 64 || record.CapturedBinary == "" || !containsPath(filepath.Join(filepath.Dir(path), "scratch"), record.CapturedBinary) {
		return invalidEvidence("captured binary or adapter identity")
	}
	captured, err := checkedPath(record.CapturedBinary, repo)
	if err != nil {
		return invalidEvidence("captured binary path")
	}
	info, err := os.Lstat(captured)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > maxCapturedBinaryBytes {
		return invalidEvidence("captured binary file")
	}
	binaryBytes, err := platform.ReadSource(captured, maxCapturedBinaryBytes)
	if err != nil {
		return err
	}
	if digest(binaryBytes) != record.BinarySHA256 {
		return invalidEvidence("captured binary digest")
	}
	byFile := map[string]manifestRow{}
	for _, file := range record.Files {
		identity := filepath.Join(file.Source, filepath.FromSlash(file.Path))
		if file.Source == "" || file.Path == "" || !containsPath(record.CorpusRoot, identity) || byFile[identity].Source != "" {
			return invalidEvidence("manifest file identity")
		}
		byFile[identity] = file
	}
	byOutcome := map[string]bool{}
	searchable := map[string]bool{}
	for _, item := range record.Outcomes {
		identity := filepath.Join(item.Source, filepath.FromSlash(item.Path))
		file, ok := byFile[identity]
		if !ok || byOutcome[identity] || file.Status == "eligible" && (file.SHA256 == "" || item.SHA256 != file.SHA256) {
			return invalidEvidence("conversion or indexed digest")
		}
		byOutcome[identity] = true
		if item.Status == "current" || item.Status == "superseded" {
			searchable[identity] = true
		}
	}
	for identity, file := range byFile {
		if file.Status == "eligible" && !byOutcome[identity] {
			return invalidEvidence("eligible file lacks conversion outcome")
		}
	}
	for _, set := range []questionSet{keywords, sentences} {
		for _, q := range set.Entries {
			for _, owner := range q.Owners {
				identity := filepath.Join(record.CorpusRoot, filepath.FromSlash(owner))
				file, ok := byFile[identity]
				if !ok || file.Status != "eligible" || !byOutcome[identity] {
					return invalidEvidence("frozen owner is missing")
				}
				proved := false
				for _, proof := range q.Evidence {
					if proof.Path == owner && proof.SHA256 == file.SHA256 {
						proved = true
					}
				}
				if !proved {
					return invalidEvidence("frozen owner digest differs")
				}
			}
		}
	}
	if len(record.Commands) != 31 || record.IndexChunks < 0 {
		return invalidEvidence("command count or chunk count")
	}
	first := record.Commands[0]
	if len(first.Argv) != 6 || first.Argv[0] != record.CapturedBinary || first.Argv[1] != "index" || first.Argv[2] != "--state-dir" || first.Argv[4] != "--format" || first.Argv[5] != "json" {
		return invalidEvidence("index command arguments")
	}
	state := first.Argv[3]
	indexedChunks := 0
	gaps, err := indexReply(first, &indexedChunks)
	if err != nil || indexedChunks != record.IndexChunks {
		return invalidEvidence("index command reply or stored chunk count")
	}
	for _, gap := range gaps {
		found := false
		for _, saved := range record.Skipped {
			if saved.Path == gap.Path && saved.Reason == gap.Reason {
				found = true
			}
		}
		if !found {
			return invalidEvidence("index gap missing from evidence")
		}
	}
	hits := 0
	for i, q := range keywords.Entries {
		row := record.Rows[i]
		if row.ID != q.ID || row.Query != q.Query || row.Status != "complete" || row.Hit == nil || row.QueryAsRun == "" || row.QueryMode == "" || row.Total < 0 || len(row.Ranked) != row.Total {
			return invalidEvidence("question row identity or rank count")
		}
		command := record.Commands[i+1]
		want := []string{record.CapturedBinary, "search", q.Query, "--limit", "100", "--no-refresh", "--state-dir", state, "--format", "json"}
		if !reflect.DeepEqual(command.Argv, want) || command.DurationMS < 0 {
			return invalidEvidence("search command arguments")
		}
		public, err := commandSearch(command)
		if err != nil {
			return invalidEvidence("public search reply")
		}
		full := search.Reply{Count: len(row.Ranked), Total: row.Total, Query: search.Query{AsRun: row.QueryAsRun, Mode: row.QueryMode}, Results: make([]search.Result, len(row.Ranked))}
		seenIDs := map[string]bool{}
		for j, rank := range row.Ranked {
			if rank.Rank != j+1 || rank.ID == "" || seenIDs[rank.ID] || !searchable[filepath.Clean(rank.Path)] {
				return invalidEvidence("complete rank identity")
			}
			seenIDs[rank.ID] = true
			for _, alias := range rank.AlsoIn {
				if !searchable[filepath.Clean(alias)] {
					return invalidEvidence("rank alias identity")
				}
			}
			full.Results[j] = search.Result{ID: rank.ID, Path: rank.Path, AlsoIn: rank.AlsoIn}
		}
		if parity(public, full) != nil {
			return invalidEvidence("public and complete ranks differ")
		}
		hit, top := score(full.Results, q.Owners, record.CorpusRoot)
		if hit != *row.Hit || !reflect.DeepEqual(top, row.TopThree) {
			return invalidEvidence("hit decision differs from complete ranks")
		}
		if hit {
			hits++
		}
	}
	if hits != record.HitCount {
		return invalidEvidence("measurement hit count differs")
	}
	return nil
}
func promote(recordPath, floorPath, repo string) error {
	if _, err := checkedPath(floorPath, repo); err != nil {
		return err
	}
	record, hash, err := readRecord(recordPath, repo)
	if err != nil {
		return err
	}
	claimPath := filepath.Join(repo, ".local", "evaluation", "claims", record.KeywordSHA256+".json")
	claimData, _, err := readControl(claimPath, repo, 1<<20)
	if err != nil {
		return err
	}
	var first claim
	if err = json.Unmarshal(claimData, &first); err != nil {
		return err
	}
	checkedResult, err := checkedPath(recordPath, repo)
	if err != nil {
		return err
	}
	if !validResultLocation(checkedResult, repo) {
		return errors.New("result is outside an owned evaluation attempt")
	}
	if err := validateAttemptOwner(checkedResult, repo, record.KeywordSHA256); err != nil {
		return err
	}
	if first.Status != "complete" || first.KeywordSHA256 != record.KeywordSHA256 || first.ResultPath != checkedResult || first.ResultSHA256 != hash {
		return errors.New("result is not the first completed measurement")
	}
	floor := floorRecord{Version: "1", KeywordSHA256: record.KeywordSHA256, SentenceSHA256: record.SentenceSHA256, ScoringVersion: scoringVersion, FirstResultSHA256: hash, HitCount: record.HitCount}
	if err = writeExclusive(floorPath, floor); err == nil {
		return nil
	}
	if !errors.Is(err, os.ErrExist) {
		return err
	}
	data, _, readErr := readControl(floorPath, repo, 1<<20)
	if readErr != nil {
		return readErr
	}
	var current floorRecord
	if readErr = json.Unmarshal(data, &current); readErr != nil {
		return readErr
	}
	if current != floor {
		return errors.New("existing floor conflicts with completed measurement")
	}
	return nil
}
func recoverClaim(path, repo string) error {
	claimFile, err := checkedPath(path, repo)
	if err != nil {
		return err
	}
	claimBase := filepath.Join(repo, ".local", "evaluation", "claims")
	if filepath.Dir(claimFile) != claimBase {
		return errors.New("measurement recovery needs a claim in the evaluator claims folder")
	}
	lock, err := platform.Acquire(filepath.Dir(claimFile), "measurement", true)
	if err != nil {
		return err
	}
	defer lock.Close()
	data, checked, err := readControl(claimFile, repo, 1<<20)
	if err != nil {
		return err
	}
	var current claim
	if err = json.Unmarshal(data, &current); err != nil {
		return err
	}
	if current.KeywordSHA256 == "" || current.ResultPath == "" {
		return errors.New("invalid measurement claim")
	}
	if filepath.Base(claimFile) != current.KeywordSHA256+".json" {
		return errors.New("claim name does not match frozen questions")
	}
	resultPath, err := checkedPath(current.ResultPath, repo)
	if err != nil {
		return err
	}
	if !validResultLocation(resultPath, repo) {
		return errors.New("claim result is outside evaluator evidence")
	}
	if err := validateAttemptOwner(resultPath, repo, current.KeywordSHA256); err != nil {
		return err
	}
	if current.Status == "complete" {
		_, hash, err := readRecord(current.ResultPath, repo)
		if err != nil {
			return err
		}
		if hash != current.ResultSHA256 {
			return errors.New("completed claim digest differs")
		}
		return nil
	}
	if current.Status == "invalid" {
		return nil
	}
	if current.Status != "running" {
		return errors.New("unknown measurement claim state")
	}
	record, hash, err := readRecord(current.ResultPath, repo)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// A missing result proves the attempt did not finish. A missing
			// dependency of an existing result leaves the first claim reserved.
			if _, statErr := os.Lstat(resultPath); statErr == nil {
				return err
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return statErr
			}
		} else if !errors.Is(err, errInvalidEvidence) {
			return err
		}
		current.Status = "invalid"
		current.ResultSHA256 = ""
		return replaceAtomic(checked, current)
	}
	if record.KeywordSHA256 != current.KeywordSHA256 {
		return errors.New("ambiguous measurement claim")
	}
	current.Status = "complete"
	current.ResultSHA256 = hash
	return replaceAtomic(checked, current)
}
func checkFloor(path, repo, keywordHash, sentenceHash string) (int, error) {
	data, _, err := readControl(path, repo, 1<<20)
	if err != nil {
		return 0, err
	}
	var floor floorRecord
	if err = json.Unmarshal(data, &floor); err != nil {
		return 0, err
	}
	if floor.Version != "1" || floor.ScoringVersion != scoringVersion || floor.KeywordSHA256 != keywordHash || floor.SentenceSHA256 != sentenceHash || floor.FirstResultSHA256 == "" || floor.HitCount < 0 || floor.HitCount > 30 {
		return 0, fmt.Errorf("floor does not bind frozen inputs")
	}
	claimPath := filepath.Join(repo, ".local", "evaluation", "claims", keywordHash+".json")
	claimData, _, err := readControl(claimPath, repo, 1<<20)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return floor.HitCount, nil
		}
		return 0, err
	}
	var first claim
	if err = json.Unmarshal(claimData, &first); err != nil {
		return 0, err
	}
	if first.Status != "complete" || first.KeywordSHA256 != keywordHash || first.ResultSHA256 != floor.FirstResultSHA256 {
		return 0, errors.New("floor differs from the first completed claim")
	}
	resultPath, err := checkedPath(first.ResultPath, repo)
	if err != nil {
		return 0, err
	}
	if !validResultLocation(resultPath, repo) {
		return 0, errors.New("first result is outside evaluator evidence")
	}
	if err := validateAttemptOwner(resultPath, repo, keywordHash); err != nil {
		return 0, err
	}
	firstResult, resultHash, err := readRecord(resultPath, repo)
	if err != nil {
		return 0, err
	}
	if resultHash != floor.FirstResultSHA256 || firstResult.HitCount != floor.HitCount || firstResult.KeywordSHA256 != keywordHash || firstResult.SentenceSHA256 != sentenceHash {
		return 0, errors.New("floor differs from the first completed result")
	}
	return floor.HitCount, nil
}

func validResultLocation(path, repo string) bool {
	base := filepath.Join(repo, ".local", "evaluation")
	attempt := filepath.Dir(path)
	return filepath.Base(path) == "result.json" && filepath.Dir(attempt) == base && filepath.Base(attempt) != "claims"
}

func validateAttemptOwner(resultPath, repo, keywordHash string) error {
	marker := filepath.Join(filepath.Dir(resultPath), "attempt-owner.json")
	data, _, err := readControl(marker, repo, 1<<20)
	if err != nil {
		return err
	}
	var owner attemptOwner
	if err = json.Unmarshal(data, &owner); err != nil {
		return err
	}
	if owner.KeywordSHA256 == "" || owner.KeywordSHA256 != keywordHash {
		return errors.New("result attempt owner differs from frozen questions")
	}
	return nil
}
