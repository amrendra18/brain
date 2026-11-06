package evaluaterun

import (
	"brain/internal/index"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func comparisonFixture(t *testing.T, binary string) (fixture, ComparisonOptions) {
	t.Helper()
	f := makeFixture(t)
	f.binary = binary
	keywordBytes, err := os.ReadFile(f.keywords)
	if err != nil {
		t.Fatal(err)
	}
	sentenceBytes, err := os.ReadFile(f.sentences)
	if err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, f.floor, floorRecord{Version: "1", KeywordSHA256: digest(keywordBytes), SentenceSHA256: digest(sentenceBytes), ScoringVersion: scoringVersion, FirstResultSHA256: strings.Repeat("a", 64), HitCount: 0})
	return f, ComparisonOptions{ConfigPath: f.config, StateRoot: filepath.Join(f.root, "comparison"), BinaryPath: f.binary, KeywordPath: f.keywords, SentencePath: f.sentences, FloorPath: f.floor, CorpusRoot: f.root}
}

func TestEvaluationAdapterCapacity(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := index.Open(state, true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	result, err := db.Exec("INSERT INTO files(path,source,project,kind,status,size,mtime_ns,sha256,namespace,chunker_version,indexed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)", "owner.md", "source-root", "project", "doc", "current", 8, 1, "raw", "source:0:", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	fileID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	insert := func(start, end int) {
		t.Helper()
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		stmt, err := tx.Prepare("INSERT INTO chunks(id,file,heading,text,bytes,dedupe_key,also_in,project,kind) VALUES(?,?,?,?,?,?,?,?,?)")
		if err != nil {
			t.Fatal(err)
		}
		for i := start; i < end; i++ {
			id := fmt.Sprintf("chunk-%05d", i)
			if _, err := stmt.Exec(id, fileID, "capacity", "capacity", 8, id, "[]", "project", "doc"); err != nil {
				t.Fatal(err)
			}
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	insert(0, 32765)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	full, count, err := completeRank(state, "capacity")
	if err != nil || count != 32765 || full.Total != 32765 || len(full.Results) != 32765 || len(distinctFiles(full.Results, filepath.Dir(state))) != 1 {
		t.Fatalf("complete 32765-chunk rank failed: count=%d total=%d ranked=%d err=%v", count, full.Total, len(full.Results), err)
	}
	db, err = index.Open(state, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	insert(32765, 32766)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	full, count, err = completeRank(state, "capacity")
	if err == nil || err.Error() != "adapter-capacity-exceeded" || count != 32766 || len(full.Results) != 0 {
		t.Fatalf("oversized adapter call was not stopped: count=%d ranked=%d err=%v", count, len(full.Results), err)
	}
}

func TestHybridCorpusCapture(t *testing.T) {
	binary := buildTarget(t)
	t.Run("equal indexes and retained fifty rows", func(t *testing.T) {
		f, opt := comparisonFixture(t, binary)
		got, err := PrepareComparison(context.Background(), f.repo, opt)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Rows) != 50 || len(got.Inventory) != got.IndexChunks || got.ManifestSHA256 == "" || got.InventorySHA256 == "" || got.LexicalDatabaseSHA == "" || got.HybridDatabaseSHA == "" || got.KeywordHits != 30 || !got.KeywordFloorMet {
			t.Fatalf("incomplete two-index evidence: %+v", got)
		}
		for i, row := range got.Rows {
			if row.Status != "complete" || row.Hit == nil || !*row.Hit || len(row.Files) == 0 || len(row.Ranks) == 0 {
				t.Fatalf("row %d is incomplete: %+v", i, row)
			}
		}
		validated, err := ValidateBaseline(f.repo, got.RecordPath)
		if err != nil || validated.RecordSHA256 != got.RecordSHA256 || len(validated.Rows) != 50 {
			t.Fatalf("read-only validation failed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(got.HybridState, "index.sqlite"), []byte("damaged hybrid copy"), 0600); err != nil {
			t.Fatal(err)
		}
		resumed, err := ValidateResumeBaseline(f.repo, got.RecordPath, got.RecordSHA256)
		if err != nil || resumed.HybridCopyValid || resumed.RecordSHA256 != got.RecordSHA256 {
			t.Fatalf("intact lexical evidence did not survive hybrid damage: %v", err)
		}
		if _, err := ValidateBaseline(f.repo, got.RecordPath); err == nil || !strings.Contains(err.Error(), "resume-corpus-invalid") {
			t.Fatalf("strict validation accepted damaged hybrid copy: %v", err)
		}
		if err := os.WriteFile(filepath.Join(got.LexicalState, "index.sqlite"), []byte("damaged"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateResumeBaseline(f.repo, got.RecordPath, got.RecordSHA256); err == nil || !strings.Contains(err.Error(), "resume-corpus-invalid") {
			t.Fatalf("damaged sealed index accepted: %v", err)
		}
	})
	for _, kind := range []string{"add", "edit", "remove"} {
		t.Run("drift-"+kind, func(t *testing.T) {
			f, opt := comparisonFixture(t, binary)
			scoringStarted := 0
			opt.beforeScoring = func() { scoringStarted++ }
			opt.issueCommand = func(d *driver, ctx context.Context, words ...string) commandRow {
				if len(words) == 0 || words[0] != "index" {
					t.Fatal("a search command ran before both captures matched")
				}
				return d.command(ctx, words...)
			}
			opt.afterFirstCapture = func() {
				switch kind {
				case "add":
					if err := os.WriteFile(filepath.Join(f.source, "new.md"), []byte("# New harbor guide\n"), 0600); err != nil {
						t.Fatal(err)
					}
				case "edit":
					if err := os.WriteFile(filepath.Join(f.source, "README.md"), []byte("# Changed harbor guide\n"), 0600); err != nil {
						t.Fatal(err)
					}
				case "remove":
					if err := os.Remove(filepath.Join(f.source, "README.md")); err != nil {
						t.Fatal(err)
					}
				}
			}
			if result, err := PrepareComparison(context.Background(), f.repo, opt); err == nil || result.RecordPath != "" {
				t.Fatalf("changed corpus accepted: %+v, %v", result, err)
			}
			if scoringStarted != 0 {
				t.Fatalf("scoring started %d times before equal captures", scoringStarted)
			}
		})
	}
}

func TestValidateBaselineRefusesTamper(t *testing.T) {
	binary := buildTarget(t)
	f, opt := comparisonFixture(t, binary)
	good, err := PrepareComparison(context.Background(), f.repo, opt)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		edit func(*Baseline)
	}{
		{"missing-row", func(b *Baseline) { b.Rows = b.Rows[:49] }},
		{"wrong-owner", func(b *Baseline) { b.Rows[0].Owners = []string{"other.md"} }},
		{"wrong-rank-id", func(b *Baseline) { b.Rows[0].Ranks[0].ID = "made-up" }},
		{"wrong-public-command", func(b *Baseline) { b.Rows[30].PublicCommand.Argv[1] = "index" }},
		{"wrong-public-result", func(b *Baseline) { b.Rows[30].PublicCommand.Stdout = `{}` }},
		{"wrong-file-order", func(b *Baseline) { b.Rows[0].Files = []string{"other.md"} }},
		{"wrong-hit", func(b *Baseline) { miss := false; b.Rows[0].Hit = &miss }},
		{"wrong-floor", func(b *Baseline) { b.FloorSHA256 = strings.Repeat("b", 64) }},
		{"wrong-adapter", func(b *Baseline) { b.AdapterSHA256 = strings.Repeat("0", 64) }},
		{"wrong-inventory", func(b *Baseline) { b.InventorySHA256 = strings.Repeat("b", 64) }},
		{"wrong-outcomes", func(b *Baseline) { b.OutcomesSHA256 = strings.Repeat("b", 64) }},
		{"wrong-database", func(b *Baseline) { b.HybridDatabaseSHA = strings.Repeat("b", 64) }},
		{"outside-state", func(b *Baseline) { b.LexicalState = f.source }},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clone := good
			clone.Rows = append([]ComparisonRow{}, good.Rows...)
			for j := range clone.Rows {
				clone.Rows[j].Owners = append([]string{}, good.Rows[j].Owners...)
				clone.Rows[j].Files = append([]string{}, good.Rows[j].Files...)
				clone.Rows[j].Ranks = append([]ComparisonRank{}, good.Rows[j].Ranks...)
				clone.Rows[j].PublicCommand = good.Rows[j].PublicCommand
				clone.Rows[j].PublicCommand.Argv = append([]string{}, good.Rows[j].PublicCommand.Argv...)
			}
			tc.edit(&clone)
			path := filepath.Join(opt.StateRoot, "tampered-"+string(rune('a'+i))+".json")
			writeTestJSON(t, path, clone)
			if _, err := ValidateBaseline(f.repo, path); err == nil {
				t.Fatal("tampered baseline accepted")
			}
		})
	}
}

func TestComparisonHelpers(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "brain")
	filled := fillComparisonOptions(repo, ComparisonOptions{})
	if filled.BinaryPath == "" || filled.KeywordPath != "" || filled.SentencePath != "" || filled.FloorPath != "" || filled.CorpusRoot != "" {
		t.Fatalf("defaults differ: %+v", filled)
	}
	custom := ComparisonOptions{BinaryPath: "binary", KeywordPath: "keywords", SentencePath: "sentences", FloorPath: "floor", CorpusRoot: "corpus"}
	if got := fillComparisonOptions(repo, custom); got.BinaryPath != custom.BinaryPath || got.KeywordPath != custom.KeywordPath || got.SentencePath != custom.SentencePath || got.FloorPath != custom.FloorPath || got.CorpusRoot != custom.CorpusRoot {
		t.Fatalf("explicit paths changed: %+v", got)
	}
	row := ComparisonRow{ID: "one", PublicCommand: commandRow{Argv: []string{"first"}}}
	other := row
	other.PublicCommand = commandRow{Argv: []string{"second"}}
	if !sameRankRows([]ComparisonRow{row}, []ComparisonRow{other}) || sameRankRows([]ComparisonRow{row}, nil) || sameRankRows([]ComparisonRow{row}, []ComparisonRow{{ID: "other"}}) {
		t.Fatal("rank equality did not separate command observations")
	}
	state := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := sealedDatabaseSHA(state, repo); err == nil {
		t.Fatal("missing database accepted")
	}
	database := filepath.Join(state, "index.sqlite")
	if err := os.WriteFile(database, []byte("owned database"), 0600); err != nil {
		t.Fatal(err)
	}
	if hash, err := sealedDatabaseSHA(state, repo); err != nil || hash != digest([]byte("owned database")) {
		t.Fatalf("sealed bytes differ: %s, %v", hash, err)
	}
	if err := os.WriteFile(database+"-wal", []byte("uncommitted"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := sealedDatabaseSHA(state, repo); err == nil {
		t.Fatal("nonempty journal accepted")
	}
	if err := os.Remove(database + "-wal"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(database+"-shm", 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := sealedDatabaseSHA(state, repo); err == nil {
		t.Fatal("nonfile sidecar accepted")
	}
}

func TestValidateComparisonCaptureRefusals(t *testing.T) {
	binary := buildTarget(t)
	f, opt := comparisonFixture(t, binary)
	baseline, err := PrepareComparison(context.Background(), f.repo, opt)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(baseline.KeywordRecordPath)
	if err != nil {
		t.Fatal(err)
	}
	var original comparisonCapture
	if err := json.Unmarshal(data, &original); err != nil {
		t.Fatal(err)
	}
	if _, err := validateComparisonCapture(original, f.repo); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*comparisonCapture){
		"version":   func(c *comparisonCapture) { c.Version = 2 },
		"exit":      func(c *comparisonCapture) { c.IndexCommand.Exit = 2 },
		"argv":      func(c *comparisonCapture) { c.IndexCommand.Argv = []string{"bad"} },
		"reply":     func(c *comparisonCapture) { c.IndexCommand.Stdout = `{}` },
		"manifest":  func(c *comparisonCapture) { c.ManifestSHA256 = "bad" },
		"outcomes":  func(c *comparisonCapture) { c.OutcomesSHA256 = "bad" },
		"binary":    func(c *comparisonCapture) { c.BinarySHA256 = "bad" },
		"database":  func(c *comparisonCapture) { c.DatabaseSHA256 = "bad" },
		"inventory": func(c *comparisonCapture) { c.InventorySHA256 = "bad" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := original
			edit(&copy)
			if _, err := validateComparisonCapture(copy, f.repo); err == nil {
				t.Fatal("tampered capture accepted")
			}
		})
	}
}

func TestUniqueJSONObject(t *testing.T) {
	for _, input := range []string{`{"a":1,"a":2}`, `{"nested":{"a":1,"a":2}}`, `{"array":[{"a":1,"a":2}]}`, `{"a":1}{"b":2}`, `[]`} {
		if err := uniqueJSONObject([]byte(input)); err == nil {
			t.Fatalf("invalid evidence accepted: %s", input)
		}
	}
	if err := uniqueJSONObject([]byte(`{"a":1,"nested":{"a":2},"array":[{"a":3}]}`)); err != nil {
		t.Fatal(err)
	}
}

func TestValidateResumeBaselineDigest(t *testing.T) {
	binary := buildTarget(t)
	t.Run("changed adapter and score", func(t *testing.T) {
		f, opt := comparisonFixture(t, binary)
		original, err := PrepareComparison(context.Background(), f.repo, opt)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateResumeBaseline(f.repo, original.RecordPath, original.RecordSHA256); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateResumeBaseline(f.repo, original.RecordPath, ""); err == nil {
			t.Fatal("resume accepted no expected digest")
		}
		for name, change := range map[string]func(*Baseline){
			"adapter": func(b *Baseline) { b.AdapterSHA256 = strings.Repeat("0", 64) },
			"score":   func(b *Baseline) { b.Rows[0].Ranks[0].Score = -12345 },
		} {
			t.Run(name, func(t *testing.T) {
				clone := original
				clone.Rows = append([]ComparisonRow{}, original.Rows...)
				clone.Rows[0].Ranks = append([]ComparisonRank{}, original.Rows[0].Ranks...)
				change(&clone)
				path := filepath.Join(opt.StateRoot, "changed-"+name+".json")
				writeTestJSON(t, path, clone)
				if _, err := ValidateResumeBaseline(f.repo, path, original.RecordSHA256); err == nil {
					t.Fatal("changed baseline accepted against original digest")
				}
				if name == "adapter" {
					changedBytes, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := ValidateResumeBaseline(f.repo, path, digest(changedBytes)); err == nil {
						t.Fatal("changed adapter accepted with a matching record digest")
					}
				}
			})
		}
	})
	t.Run("score beyond public prefix", func(t *testing.T) {
		f, opt := comparisonFixture(t, binary)
		var body strings.Builder
		for i := 0; i < 120; i++ {
			fmt.Fprintf(&body, "# Section %03d\n\nharbor guide details %03d\n\n", i, i)
		}
		file := filepath.Join(f.source, "README.md")
		if err := os.WriteFile(file, []byte(body.String()), 0600); err != nil {
			t.Fatal(err)
		}
		for _, questionFile := range []string{f.keywords, f.sentences} {
			data, err := os.ReadFile(questionFile)
			if err != nil {
				t.Fatal(err)
			}
			var set questionSet
			if err := json.Unmarshal(data, &set); err != nil {
				t.Fatal(err)
			}
			for i := range set.Entries {
				set.Entries[i].Evidence[0].SHA256 = digest([]byte(body.String()))
			}
			writeTestJSON(t, questionFile, set)
		}
		keywordBytes, err := os.ReadFile(f.keywords)
		if err != nil {
			t.Fatal(err)
		}
		sentenceBytes, err := os.ReadFile(f.sentences)
		if err != nil {
			t.Fatal(err)
		}
		writeTestJSON(t, f.floor, floorRecord{Version: "1", KeywordSHA256: digest(keywordBytes), SentenceSHA256: digest(sentenceBytes), ScoringVersion: scoringVersion, FirstResultSHA256: strings.Repeat("a", 64), HitCount: 0})
		original, err := PrepareComparison(context.Background(), f.repo, opt)
		if err != nil {
			t.Fatal(err)
		}
		if len(original.Rows[0].Ranks) <= 100 {
			t.Fatalf("fixture has only %d ranks", len(original.Rows[0].Ranks))
		}
		clone := original
		clone.Rows = append([]ComparisonRow{}, original.Rows...)
		clone.Rows[0].Ranks = append([]ComparisonRank{}, original.Rows[0].Ranks...)
		clone.Rows[0].Ranks[100].Score = -12345
		path := filepath.Join(opt.StateRoot, "changed-rank-101.json")
		writeTestJSON(t, path, clone)
		if _, err := ValidateResumeBaseline(f.repo, path, original.RecordSHA256); err == nil {
			t.Fatal("rank 101 score change accepted")
		}
	})
}

func TestHybridSentenceFailurePreservesFloor(t *testing.T) {
	binary := buildTarget(t)
	f, opt := comparisonFixture(t, binary)
	floorBefore, err := os.ReadFile(f.floor)
	if err != nil {
		t.Fatal(err)
	}
	indexCommands, searchCommands := 0, 0
	opt.issueCommand = func(d *driver, ctx context.Context, words ...string) commandRow {
		if len(words) > 0 && words[0] == "index" {
			indexCommands++
		}
		if len(words) > 0 && words[0] == "search" {
			searchCommands++
			if searchCommands == 33 {
				args := append([]string{d.binary}, words...)
				args = append(args, "--state-dir", d.state, "--format", "json")
				return commandRow{Argv: args, Exit: 1, Stdout: "", Stderr: "synthetic sentence failure"}
			}
		}
		return d.command(ctx, words...)
	}
	partial, err := PrepareComparison(context.Background(), f.repo, opt)
	if err == nil || partial.RecordPath == "" || partial.RecordSHA256 == "" || partial.Status != "partial" || partial.KeywordHits != 30 || !partial.KeywordFloorMet || len(partial.Rows) != 50 {
		t.Fatalf("sentence failure lost baseline evidence: status=%s rows=%d hits=%d record=%s err=%v", partial.Status, len(partial.Rows), partial.KeywordHits, partial.RecordPath, err)
	}
	if indexCommands != 2 || searchCommands != 33 {
		t.Fatalf("unexpected commands before failure: indexes=%d searches=%d", indexCommands, searchCommands)
	}
	for i, row := range partial.Rows {
		want := "complete"
		if i == 32 {
			want = "failed"
		} else if i > 32 {
			want = "unrun"
		}
		if row.Status != want || (i < 32 && (row.Hit == nil || !*row.Hit)) || (i >= 32 && row.Hit != nil) {
			t.Fatalf("row %d status/hit differs: %s %v", i, row.Status, row.Hit)
		}
	}
	if partial.KeywordSHA256 == "" || partial.SentenceSHA256 == "" || partial.ManifestSHA256 == "" || partial.InventorySHA256 == "" {
		t.Fatal("partial capture lost frozen identities")
	}
	floorAfter, err := os.ReadFile(f.floor)
	if err != nil || !bytes.Equal(floorBefore, floorAfter) || partial.FloorSHA256 != digest(floorBefore) {
		t.Fatal("signed floor changed", err)
	}
	beforeResumeIndexes, beforeResumeSearches := indexCommands, searchCommands
	retained, err := ValidateResumeBaseline(f.repo, partial.RecordPath, partial.RecordSHA256)
	if err == nil || !strings.Contains(err.Error(), "resume-baseline-incomplete") || !strings.Contains(err.Error(), "S03") || !strings.Contains(err.Error(), "S20") || retained.RecordSHA256 != partial.RecordSHA256 {
		t.Fatalf("resume did not name incomplete sentence rows: record=%s err=%v", retained.RecordSHA256, err)
	}
	if indexCommands != beforeResumeIndexes || searchCommands != beforeResumeSearches {
		t.Fatal("resume ran an index or lexical command")
	}
	floorAfterResume, err := os.ReadFile(f.floor)
	if err != nil || !bytes.Equal(floorBefore, floorAfterResume) {
		t.Fatal("resume changed the signed floor", err)
	}
}

func TestHybridSecondCaptureSentenceFailurePreservesBaseline(t *testing.T) {
	binary := buildTarget(t)
	f, opt := comparisonFixture(t, binary)
	floorBefore, err := os.ReadFile(f.floor)
	if err != nil {
		t.Fatal(err)
	}
	indexCommands, searchCommands := 0, 0
	opt.issueCommand = func(d *driver, ctx context.Context, words ...string) commandRow {
		if len(words) > 0 && words[0] == "index" {
			indexCommands++
		}
		if len(words) > 0 && words[0] == "search" {
			searchCommands++
			if searchCommands == 83 {
				args := append([]string{d.binary}, words...)
				args = append(args, "--state-dir", d.state, "--format", "json")
				return commandRow{Argv: args, Exit: 1, Stderr: "synthetic second-capture sentence failure"}
			}
		}
		return d.command(ctx, words...)
	}
	partial, err := PrepareComparison(context.Background(), f.repo, opt)
	if err == nil || partial.RecordPath == "" || partial.RecordSHA256 == "" || partial.Status != "partial" || len(partial.Rows) != 50 || len(partial.SecondRows) != 50 || partial.KeywordHits != 30 || !partial.KeywordFloorMet || !strings.HasPrefix(partial.Failure, "second capture: ") {
		t.Fatalf("second-capture failure lost baseline: status=%s rows=%d second=%d record=%s err=%v", partial.Status, len(partial.Rows), len(partial.SecondRows), partial.RecordPath, err)
	}
	for i, row := range partial.Rows {
		if row.Status != "complete" || row.Hit == nil || !*row.Hit {
			t.Fatalf("first capture row %d was lost", i)
		}
	}
	for i, row := range partial.SecondRows {
		want := "complete"
		if i == 32 {
			want = "failed"
		} else if i > 32 {
			want = "unrun"
		}
		if row.Status != want || (i < 32 && row.Hit == nil) || (i >= 32 && row.Hit != nil) {
			t.Fatalf("second capture row %d differs: %s %v", i, row.Status, row.Hit)
		}
	}
	if indexCommands != 2 || searchCommands != 83 {
		t.Fatalf("unexpected commands: indexes=%d searches=%d", indexCommands, searchCommands)
	}
	floorAfter, err := os.ReadFile(f.floor)
	if err != nil || !bytes.Equal(floorBefore, floorAfter) || partial.FloorSHA256 != digest(floorBefore) {
		t.Fatal("floor changed", err)
	}
	beforeResumeIndexes, beforeResumeSearches := indexCommands, searchCommands
	retained, err := ValidateResumeBaseline(f.repo, partial.RecordPath, partial.RecordSHA256)
	if err == nil || !strings.Contains(err.Error(), "resume-baseline-incomplete: second capture") || !strings.Contains(err.Error(), "S03") || !strings.Contains(err.Error(), "S20") || retained.RecordSHA256 != partial.RecordSHA256 {
		t.Fatalf("resume did not name second-capture missing rows: retained=%s err=%v", retained.RecordSHA256, err)
	}
	if indexCommands != beforeResumeIndexes || searchCommands != beforeResumeSearches {
		t.Fatal("resume ran index or search")
	}
}
