package evaluaterun

import (
	"brain/internal/index"
	"brain/internal/search"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// ValidateBaseline checks both sealed captures without index or search calls.
func ValidateBaseline(repo, recordPath string) (Baseline, error) {
	return validateComparisonBaseline(repo, recordPath, true, "")
}

// ValidateResumeBaseline requires the lexical capture and all fifty rows.
// The original comparison record's digest authenticates every retained rank,
// including ranks beyond the public command's first one hundred results.
// It reports hybrid-copy damage so model work can use the intact lexical copy.
func ValidateResumeBaseline(repo, recordPath, expectedSHA256 string) (Baseline, error) {
	return validateComparisonBaseline(repo, recordPath, false, expectedSHA256)
}

func readComparisonCapture(path, repo, want string) (comparisonCapture, error) {
	var capture comparisonCapture
	data, _, err := readControl(path, repo, 512<<20)
	if err != nil || digest(data) != want {
		return capture, errors.New("baseline capture record differs")
	}
	if err := uniqueJSONObject(data); err != nil {
		return capture, err
	}
	if err := json.Unmarshal(data, &capture); err != nil {
		return capture, err
	}
	return capture, nil
}

func uniqueJSONObject(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var walk func() error
	walk = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok || seen[key] {
					return errors.New("duplicate or invalid JSON object key")
				}
				seen[key] = true
				if err := walk(); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := walk(); err != nil {
					return err
				}
			}
		default:
			return errors.New("invalid JSON delimiter")
		}
		_, err = decoder.Token()
		return err
	}
	first, err := decoder.Token()
	if err != nil {
		return err
	}
	if first != json.Delim('{') {
		return errors.New("evidence must be one JSON object")
	}
	seen := map[string]bool{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok || seen[key] {
			return errors.New("duplicate or invalid JSON object key")
		}
		seen[key] = true
		if err := walk(); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON evidence")
	}
	return nil
}

func validateComparisonBaseline(repo, recordPath string, requireHybrid bool, expectedSHA256 string) (Baseline, error) {
	var baseline Baseline
	abs, err := filepath.Abs(repo)
	if err != nil {
		return baseline, err
	}
	repo = systemAlias(abs)
	checked, err := checkedPath(recordPath, repo)
	if err != nil {
		return baseline, err
	}
	data, _, err := readControl(checked, repo, 512<<20)
	if err != nil {
		return baseline, err
	}
	if !requireHybrid {
		if len(expectedSHA256) != 64 {
			return baseline, errors.New("expected baseline digest is required for resume")
		}
		if _, err := hex.DecodeString(expectedSHA256); err != nil || digest(data) != expectedSHA256 {
			return baseline, errors.New("baseline digest differs from original comparison")
		}
	}
	if err := uniqueJSONObject(data); err != nil {
		return baseline, err
	}
	if err := json.Unmarshal(data, &baseline); err != nil {
		return baseline, err
	}
	if baseline.SchemaVersion != 1 || baseline.Repo != repo || len(baseline.Rows) != 50 || baseline.IndexChunks < 0 || baseline.IndexChunks > 32765 || baseline.Status != "complete" && baseline.Status != "partial" {
		return baseline, errors.New("unsupported or incomplete baseline record")
	}
	selfPath, err := os.Executable()
	if err != nil {
		return baseline, err
	}
	selfBytes, _, err := readControl(selfPath, repo, 64<<20)
	if err != nil || digest(selfBytes) != baseline.AdapterSHA256 {
		return baseline, errors.New("baseline adapter identity differs")
	}
	owned := filepath.Dir(checked)
	if baseline.LexicalState != filepath.Join(owned, "lexical") || baseline.HybridState != filepath.Join(owned, "hybrid") || baseline.KeywordRecordPath != filepath.Join(owned, "capture-lexical.json") || baseline.SecondRecordPath != filepath.Join(owned, "capture-hybrid.json") {
		return baseline, errors.New("baseline paths are outside the owned capture")
	}
	for _, pair := range []struct {
		bytes []byte
		hash  string
	}{{baseline.KeywordSnapshot, baseline.KeywordSHA256}, {baseline.SentenceSnapshot, baseline.SentenceSHA256}, {baseline.ConfigSnapshot, baseline.ConfigSHA256}} {
		if len(pair.bytes) == 0 || digest(pair.bytes) != pair.hash {
			return baseline, errors.New("baseline frozen snapshot differs")
		}
	}
	var keywords, sentences questionSet
	if json.Unmarshal(baseline.KeywordSnapshot, &keywords) != nil || json.Unmarshal(baseline.SentenceSnapshot, &sentences) != nil || validateQuestionSet(keywords, 30, true) != nil || validateQuestionSet(sentences, 20, false) != nil || keywords.Status != "frozen; unscored" || sentences.Status != "frozen; unscored" {
		return baseline, errors.New("baseline question sets are invalid")
	}
	first, err := readComparisonCapture(baseline.KeywordRecordPath, repo, baseline.KeywordRecordSHA)
	if err != nil {
		return baseline, err
	}
	second, err := readComparisonCapture(baseline.SecondRecordPath, repo, baseline.SecondRecordSHA)
	if err != nil {
		return baseline, err
	}
	if first.State != baseline.LexicalState || second.State != baseline.HybridState || first.ManifestSHA256 != baseline.ManifestSHA256 || second.ManifestSHA256 != baseline.ManifestSHA256 || first.OutcomesSHA256 != baseline.OutcomesSHA256 || second.OutcomesSHA256 != baseline.OutcomesSHA256 || first.InventorySHA256 != baseline.InventorySHA256 || second.InventorySHA256 != baseline.InventorySHA256 || first.BinarySHA256 != baseline.BinarySHA256 || second.BinarySHA256 != baseline.BinarySHA256 || first.IndexChunks != baseline.IndexChunks || second.IndexChunks != baseline.IndexChunks || first.DatabaseSHA256 != baseline.LexicalDatabaseSHA || second.DatabaseSHA256 != baseline.HybridDatabaseSHA || first.EffectiveConfigSHA != second.EffectiveConfigSHA || !reflect.DeepEqual(first.IndexGaps, second.IndexGaps) || !reflect.DeepEqual(first.Files, second.Files) || !reflect.DeepEqual(first.Outcomes, second.Outcomes) {
		return baseline, errors.New("baseline capture identities differ")
	}
	inventory, err := validateComparisonCapture(first, repo)
	if err != nil {
		return baseline, err
	}
	if _, err := validateComparisonCapture(second, repo); err != nil {
		if requireHybrid {
			return baseline, err
		}
		baseline.HybridCopyValid = false
	} else {
		baseline.HybridCopyValid = true
	}
	floorBytes, _, err := readControl(baseline.FloorPath, repo, 1<<20)
	if err != nil || digest(floorBytes) != baseline.FloorSHA256 {
		return baseline, errors.New("baseline floor changed")
	}
	minimum, err := checkFloor(baseline.FloorPath, repo, baseline.KeywordSHA256, baseline.SentenceSHA256)
	if err != nil || minimum != baseline.FloorMinimum {
		return baseline, errors.New("baseline floor outcome differs")
	}
	byChunk := make(map[string]index.InventoryChunk, len(inventory))
	for _, chunk := range inventory {
		byChunk[chunk.ID] = chunk
	}
	hits := 0
	missing := []string{}
	for i, row := range baseline.Rows {
		q := question{}
		set := "keyword"
		if i < 30 {
			q = keywords.Entries[i]
		} else {
			q = sentences.Entries[i-30]
			set = "sentence"
		}
		queryText := q.Query
		if set == "sentence" {
			queryText = q.Question
		}
		if row.Set != set || row.ID != q.ID || row.Query != queryText || !reflect.DeepEqual(row.Owners, q.Owners) || row.ExactName != *q.ExactName {
			return baseline, errors.New("baseline question identity differs")
		}
		if row.Status == "failed" || row.Status == "unrun" {
			if row.Hit != nil || row.Ranks != nil || row.Files != nil || row.TopThree != nil || row.Mode != "" || row.AsRun != "" || row.Total != 0 {
				return baseline, errors.New("incomplete row contains a scored result")
			}
			missing = append(missing, row.ID)
			continue
		}
		if row.Status != "complete" || row.Hit == nil || row.Ranks == nil || row.Files == nil || row.TopThree == nil || row.Mode == "" || row.AsRun == "" || row.Total != len(row.Ranks) {
			return baseline, fmt.Errorf("resume-baseline-incomplete: row %d", i+1)
		}
		wantArgv := []string{first.CapturedBinary, "search", queryText, "--limit", "100", "--no-refresh", "--state-dir", baseline.LexicalState, "--format", "json"}
		if !reflect.DeepEqual(row.PublicCommand.Argv, wantArgv) {
			return baseline, errors.New("baseline public command differs")
		}
		public, err := commandSearch(row.PublicCommand)
		if err != nil {
			return baseline, errors.New("baseline public search reply is invalid")
		}
		seen := map[string]bool{}
		results := make([]search.Result, len(row.Ranks))
		for j, rank := range row.Ranks {
			chunk, found := byChunk[rank.ID]
			aliases := make([]string, len(chunk.AlsoIn))
			for k, alias := range chunk.AlsoIn {
				aliases[k] = alias.Absolute()
			}
			if rank.ID == "" || seen[rank.ID] || !found || rank.Path != chunk.Primary.Absolute() || !reflect.DeepEqual(rank.AlsoIn, aliases) {
				return baseline, errors.New("baseline rank identity is invalid")
			}
			seen[rank.ID] = true
			results[j] = search.Result{ID: rank.ID, Path: rank.Path, AlsoIn: rank.AlsoIn}
		}
		full := search.Reply{Count: len(results), Total: row.Total, Query: search.Query{AsRun: row.AsRun, Mode: row.Mode}, Results: results}
		if err := parity(public, full); err != nil {
			return baseline, errors.New("baseline public prefix differs")
		}
		files := distinctFiles(results, baseline.CorpusRoot)
		if !reflect.DeepEqual(files, row.Files) {
			return baseline, errors.New("baseline file ranks differ")
		}
		hit, top := score(results, row.Owners, baseline.CorpusRoot)
		if hit != *row.Hit || !reflect.DeepEqual(top, row.TopThree) {
			return baseline, errors.New("baseline scorer differs")
		}
		if i < 30 && hit {
			hits++
		}
	}
	if hits != baseline.KeywordHits || baseline.KeywordFloorMet != (hits >= minimum) {
		return baseline, errors.New("baseline keyword floor outcome differs")
	}
	if len(missing) > 0 {
		if baseline.Status != "partial" || baseline.Failure == "" || len(baseline.SecondRows) != 0 {
			return baseline, errors.New("incomplete baseline status differs")
		}
		baseline.RecordPath = checked
		baseline.RecordSHA256 = digest(data)
		baseline.Inventory = inventory
		return baseline, fmt.Errorf("resume-baseline-incomplete: %s", strings.Join(missing, ", "))
	}
	if baseline.Status == "partial" && len(baseline.SecondRows) == 50 {
		if !strings.HasPrefix(baseline.Failure, "second capture: ") {
			return baseline, errors.New("second-capture failure reason differs")
		}
		secondMissing := []string{}
		for i, row := range baseline.SecondRows {
			q := question{}
			set := "keyword"
			if i < 30 {
				q = keywords.Entries[i]
			} else {
				q = sentences.Entries[i-30]
				set = "sentence"
			}
			queryText := q.Query
			if set == "sentence" {
				queryText = q.Question
			}
			if row.Set != set || row.ID != q.ID || row.Query != queryText || !reflect.DeepEqual(row.Owners, q.Owners) || row.ExactName != *q.ExactName {
				return baseline, errors.New("second-capture question identity differs")
			}
			if row.Status == "failed" || row.Status == "unrun" {
				if row.Hit != nil || row.Ranks != nil || row.Files != nil || row.TopThree != nil {
					return baseline, errors.New("incomplete second-capture row has a score")
				}
				secondMissing = append(secondMissing, row.ID)
			} else if row.Status != "complete" || row.Hit == nil || row.Ranks == nil || row.Files == nil || row.TopThree == nil {
				return baseline, errors.New("second-capture row status differs")
			}
		}
		if len(secondMissing) == 0 {
			return baseline, errors.New("partial second capture has no incomplete row")
		}
		baseline.RecordPath = checked
		baseline.RecordSHA256 = digest(data)
		baseline.Inventory = inventory
		return baseline, fmt.Errorf("resume-baseline-incomplete: second capture %s", strings.Join(secondMissing, ", "))
	}
	if baseline.Status != "complete" || baseline.Failure != "" {
		return baseline, errors.New("completed baseline status differs")
	}
	baseline.RecordPath = checked
	baseline.RecordSHA256 = digest(data)
	baseline.Inventory = inventory
	return baseline, nil
}
