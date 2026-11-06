package evaluaterun

import (
	"brain/internal/corpus"
	"brain/internal/search"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

type fixture struct{ repo, root, source, config, keywords, sentences, floor, binary string }

func makeFixture(t *testing.T) fixture {
	t.Helper()
	base := systemAlias(t.TempDir())
	f := fixture{root: base, repo: filepath.Join(base, "brain"), source: filepath.Join(base, "alpha"), config: filepath.Join(base, "config.json"), keywords: filepath.Join(base, "keywords.json"), sentences: filepath.Join(base, "sentences.json"), floor: filepath.Join(base, "floor.json")}
	for _, dir := range []string{f.repo, f.source} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	body := []byte("# Alpha harbor guide\n\nThe harbor guide explains the alpha mooring.\n")
	if err := os.WriteFile(filepath.Join(f.source, "README.md"), body, 0600); err != nil {
		t.Fatal(err)
	}
	config := corpus.Config{Sources: []corpus.Source{{Path: f.source, Project: "alpha", Literal: true}}, Options: corpus.Options{MaxFileBytes: 1048576, InlineRefreshLimit: 200, PurgeAfterDays: 7}}
	writeTestJSON(t, f.config, config)
	keyword := questionSet{SchemaVersion: 1, Status: "frozen; unscored", ScoringRule: scoringVersion, ExactNameRule: "literal submitted name rule"}
	sentence := questionSet{SchemaVersion: 1, Status: "frozen; unscored", ScoringRule: scoringVersion, ExactNameRule: "literal submitted name rule"}
	for i := 0; i < 30; i++ {
		q := question{ID: fmt.Sprintf("K%02d", i+1), Question: "Where is the alpha harbor guide?", Query: "harbor guide", Keywords: []string{"harbor", "guide"}, Owners: []string{"alpha/README.md"}, ExactName: boolPointer(false), ExactNameReason: "The submitted query has no exact named item."}
		q.Evidence = append(q.Evidence, struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		}{"alpha/README.md", digest(body)})
		keyword.Entries = append(keyword.Entries, q)
	}
	for i := 0; i < 20; i++ {
		q := question{ID: fmt.Sprintf("S%02d", i+1), Question: "Where can I read about the alpha harbor?", Owners: []string{"alpha/README.md"}, ExactName: boolPointer(i < 8), ExactNameReason: "The label describes the submitted sentence."}
		if i < 8 {
			q.ExactNameAnchor = "alpha harbor"
		}
		q.Evidence = append(q.Evidence, struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		}{"alpha/README.md", digest(body)})
		sentence.Entries = append(sentence.Entries, q)
	}
	writeTestJSON(t, f.keywords, keyword)
	writeTestJSON(t, f.sentences, sentence)
	return f
}
func boolPointer(value bool) *bool { return &value }

func TestEvaluationRequiresCallerInputs(t *testing.T) {
	f := makeFixture(t)
	code, output := runTest(t, f, "--config", f.config, "--state-dir", filepath.Join(f.root, "measurement"))
	if code != 2 || !strings.Contains(output, "--corpus-root DIR") {
		t.Fatalf("missing evaluation inputs were accepted: exit %d, output %q", code, output)
	}
}
func writeTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
func args(f fixture, state string) []string {
	return []string{"--config", f.config, "--state-dir", state, "--binary", f.binary, "--questions", f.keywords, "--sentences", f.sentences, "--floor", f.floor, "--corpus-root", f.root}
}
func runTest(t *testing.T, f fixture, argv ...string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	code := Run(context.Background(), f.repo, argv, &out)
	return code, out.String()
}
func buildTarget(t *testing.T) string {
	t.Helper()
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "brain")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	goBin := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBin += ".exe"
	}
	cmd := exec.Command(goBin, "build", "-trimpath", "-buildvcs=false", "-o", path, "./cmd/brain")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixture target: %v: %s", err, out)
	}
	return path
}
func wrappedTarget(t *testing.T, target, mode string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("wrapper execution requires a native fixture host")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "wrapper.go")
	body := fmt.Sprintf(`package main
import("os";"os/exec";"encoding/json")
func main(){
 cmd:=exec.Command(%q,os.Args[1:]...)
 cmd.Stderr=os.Stderr
 data,err:=cmd.Output()
 code:=0
 if err!=nil {if e,ok:=err.(*exec.ExitError);ok{code=e.ExitCode()}else{code=127}}
 if len(os.Args)>1 && ((%q=="empty-search"&&os.Args[1]=="search")||(%q=="index-chunks"&&os.Args[1]=="index")) && code==0 {
  var reply map[string]any
  if json.Unmarshal(data,&reply)!=nil{os.Exit(125)}
  if %q=="empty-search"{reply["results"]=[]any{};reply["count"]=0;reply["empty"]=true}else{reply["chunks"]=reply["chunks"].(float64)+1}
  data,_=json.Marshal(reply)
 }
 os.Stdout.Write(data)
 os.Exit(code)
}`, target, mode, mode, mode)
	if err := os.WriteFile(source, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "wrapper")
	cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", binary, source)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	return binary
}
func firstResult(t *testing.T, f fixture) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(f.repo, ".local", "evaluation", "*-*", "result.json"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("result paths %v: %v", matches, err)
	}
	return matches[0]
}

func TestEvaluationThirtyQuestions(t *testing.T) {
	f := makeFixture(t)
	f.binary = buildTarget(t)
	argv := append(args(f, filepath.Join(f.root, "state-first")), "--measure")
	if code, out := runTest(t, f, argv...); code != 1 || !strings.Contains(out, "results: 30/30") {
		t.Fatalf("first measurement: %d %s", code, out)
	}
	result := firstResult(t, f)
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relativeResult, err := filepath.Rel(workingDir, result)
	if err != nil {
		t.Fatal(err)
	}
	if code, out := runTest(t, f, "--promote-floor", relativeResult, "--floor", f.floor); code != 0 {
		t.Fatalf("promote: %d %s", code, out)
	}
	if code, out := runTest(t, f, args(f, filepath.Join(f.root, "state-repeat"))...); code != 0 || !strings.Contains(out, "results: 30/30") {
		t.Fatalf("repeat: %d %s", code, out)
	}
}
func TestEvaluationFrozenSets(t *testing.T) {
	f := makeFixture(t)
	set := questionSet{}
	data, _ := os.ReadFile(f.sentences)
	json.Unmarshal(data, &set)
	if err := validateQuestionSet(set, 20, false); err != nil {
		t.Fatal("literal true anchors rejected", err)
	}
	set.Status = "proposed; unscored"
	writeTestJSON(t, f.sentences, set)
	if code, _ := runTest(t, f, append(args(f, filepath.Join(f.root, "state")), "--measure")...); code != 2 {
		t.Fatal("proposed sentence set was accepted")
	}
	if _, err := os.Stat(filepath.Join(f.root, "state")); !os.IsNotExist(err) {
		t.Fatal("state created before freeze refusal")
	}
	keywordData, err := os.ReadFile(f.keywords)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := loadQuestions(f.keywords, f.repo, 30, true); err != nil {
		t.Fatal("explicit false labels rejected", err)
	}
	for name, edit := range map[string]func(map[string]any){
		"schema":         func(doc map[string]any) { doc["schema_version"] = 999 },
		"scoring":        func(doc map[string]any) { doc["scoring_rule"] = "top-1000-chunks" },
		"missing-label":  func(doc map[string]any) { delete(doc["entries"].([]any)[0].(map[string]any), "exact_name") },
		"missing-reason": func(doc map[string]any) { doc["entries"].([]any)[0].(map[string]any)["exact_name_reason"] = "" },
		"false-anchor": func(doc map[string]any) {
			doc["entries"].([]any)[0].(map[string]any)["exact_name_anchor"] = "unexpected"
		},
	} {
		doc := map[string]any{}
		if err := json.Unmarshal(keywordData, &doc); err != nil {
			t.Fatal(err)
		}
		doc["status"] = "frozen; unscored"
		edit(doc)
		writeTestJSON(t, f.keywords, doc)
		if _, _, _, err := loadQuestions(f.keywords, f.repo, 30, true); err == nil {
			t.Fatalf("%s frozen input accepted", name)
		}
	}
}
func TestEvaluationOwnersPresent(t *testing.T) {
	f := makeFixture(t)
	set := questionSet{}
	data, _ := os.ReadFile(f.keywords)
	json.Unmarshal(data, &set)
	set.Entries[0].Owners = []string{"alpha/missing.md"}
	writeTestJSON(t, f.keywords, set)
	config, _, _, _, err := loadConfig(f.config, f.repo)
	if err != nil {
		t.Fatal(err)
	}
	rows, _, found, err := scanManifest(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ownerMap(set, f.root, found, rows); err == nil {
		t.Fatal("missing owner accepted")
	}
}
func TestEvaluationExcludesAnswers(t *testing.T) {
	f := makeFixture(t)
	f.binary = filepath.Join(f.root, "missing-tool")
	config, _, _, _, err := loadConfig(f.config, f.repo)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(f.source, "PLAN.md"), []byte("ordinary project plan"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(f.source, "REPORT.md"), []byte("ordinary project report"), 0600); err != nil {
		t.Fatal(err)
	}
	rows, _, _, err := scanManifest(config)
	if err != nil {
		t.Fatal(err)
	}
	eligible := map[string]bool{}
	for _, row := range rows {
		if row.Status == "eligible" {
			eligible[row.Path] = true
		}
	}
	if !eligible["PLAN.md"] || !eligible["REPORT.md"] {
		t.Fatal("ordinary same-named documents were hidden")
	}
	renamed := filepath.Join(f.root, "renamed-checkout")
	if err := os.Rename(f.repo, renamed); err != nil {
		t.Fatal(err)
	}
	f.repo = renamed
	docs := filepath.Join(renamed, "docs")
	if err := os.Mkdir(docs, 0700); err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{"ancestor": f.root, "checkout": renamed, "subfolder": docs} {
		selected := config
		selected.Sources = []corpus.Source{{Path: source, Project: "test", Literal: true}}
		writeTestJSON(t, f.config, selected)
		if _, _, _, _, err = loadConfig(f.config, f.repo); err == nil {
			t.Fatalf("%s source entered evaluator checkout", name)
		}
		state := filepath.Join(f.root, "state-"+name)
		if code, _ := runTest(t, f, append(args(f, state), "--measure")...); code != 2 {
			t.Fatalf("%s source reached scoring: %d", name, code)
		}
		if _, err := os.Stat(state); !os.IsNotExist(err) {
			t.Fatalf("%s created state before refusal", name)
		}
	}
}
func TestEvaluationEligibleControlNoContentRead(t *testing.T) {
	f := makeFixture(t)
	data, err := os.ReadFile(f.keywords)
	if err != nil {
		t.Fatal(err)
	}
	f.keywords = filepath.Join(f.source, "answers.md")
	if err := os.WriteFile(f.keywords, data, 0600); err != nil {
		t.Fatal(err)
	}
	ordinary := filepath.Join(f.source, "PLAN.md")
	if err := os.WriteFile(ordinary, []byte("ordinary allowed plan"), 0600); err != nil {
		t.Fatal(err)
	}
	f.binary = filepath.Join(f.root, "missing-tool")
	old := readCorpusSource
	contentReads := 0
	readCorpusSource = func(path string, limit int64) ([]byte, error) { contentReads++; return old(path, limit) }
	defer func() { readCorpusSource = old }()
	state := filepath.Join(f.root, "state")
	if code, out := runTest(t, f, append(args(f, state), "--measure")...); code != 2 || !strings.Contains(out, "control file is eligible") {
		t.Fatal(code, out)
	}
	if contentReads != 0 {
		t.Fatal("source contents opened before eligible control refusal", contentReads)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("state created before refusal")
	}
	config, _, _, _, err := loadConfig(f.config, f.repo)
	if err != nil {
		t.Fatal(err)
	}
	found, _, err := discoverManifest(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := found[ordinary]; !ok {
		t.Fatal("ordinary control document was hidden")
	}
}
func TestEvaluationDistinctFileTopThree(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sources")
	paths := []string{"a.md", "b.md", "c.md", "d.md"}
	mk := func(name string) string { return filepath.Join(root, name) }
	results := []search.Result{{ID: "1", Path: mk(paths[0]), AlsoIn: []string{mk(paths[1])}}, {ID: "2", Path: mk(paths[0]), AlsoIn: []string{mk(paths[2])}}, {ID: "3", Path: mk(paths[3])}}
	if hit, top := score(results, []string{"c.md"}, root); !hit || len(top) != 3 || top[2] != "c.md" {
		t.Fatal("third distinct owner missed", top)
	}
	if hit, top := score(results, []string{"d.md"}, root); hit || len(top) != 3 {
		t.Fatal("fourth distinct owner counted", top)
	}
}
func TestEvaluationDeterminism(t *testing.T) {
	root := t.TempDir()
	results := []search.Result{{ID: "first", Path: filepath.Join(root, "a.md"), AlsoIn: []string{filepath.Join(root, "c.md"), filepath.Join(root, "b.md")}}}
	_, top := score(results, []string{"b.md"}, root)
	if strings.Join(top, ",") != "a.md,b.md,c.md" {
		t.Fatal("alias order", top)
	}
	public := search.Reply{Total: 1, Count: 1, Query: search.Query{AsRun: "hello", Mode: "and"}, Results: results}
	if err := parity(public, public); err != nil {
		t.Fatal(err)
	}
	other := public
	other.Results = []search.Result{{ID: "other", Path: results[0].Path, AlsoIn: results[0].AlsoIn}}
	if err := parity(public, other); err == nil {
		t.Fatal("prefix mismatch accepted")
	}
	empty := public
	empty.Count = 0
	empty.Empty = true
	empty.Results = []search.Result{}
	if err := parity(empty, public); err == nil {
		t.Fatal("empty public prefix accepted against one complete owner")
	}
	if err := validateCitations(results, map[string]bool{filepath.Clean(results[0].Path): true}); err == nil {
		t.Fatal("unknown alias citation accepted")
	}
	if _, err := commandSearch(commandRow{Exit: 0, Stdout: `{"count":0}`}); err == nil {
		t.Fatal("malformed reply accepted")
	}
}
func TestEvaluationPublicPrefixRun(t *testing.T) {
	f := makeFixture(t)
	f.binary = wrappedTarget(t, buildTarget(t), "empty-search")
	if code, out := runTest(t, f, append(args(f, filepath.Join(f.root, "state")), "--measure")...); code != 1 || strings.Contains(out, "results: 30/30") {
		t.Fatal("empty public replies established a floor", code, out)
	}
	if _, err := os.Stat(f.floor); !os.IsNotExist(err) {
		t.Fatal("floor appeared after truncated replies")
	}
}
func TestEvaluationIndexReply(t *testing.T) {
	allowed := commandRow{Exit: 1, Stdout: `{"skipped":[{"path":"x.md","reason":"conversion-failed"}],"count":1,"total":1,"chunks":2,"duration_ms":3}`}
	got := 0
	gaps, err := indexReply(allowed, &got)
	if err != nil || got != 2 || len(gaps) != 1 {
		t.Fatal("validated gap index refused", got, gaps, err)
	}
	for name, text := range map[string]string{
		"error-envelope":    `{"error":"state-unsafe","skipped":[{"path":"x","reason":"gap"}],"count":1,"total":1,"chunks":2,"duration_ms":3}`,
		"null-chunks":       `{"skipped":[],"count":0,"total":0,"chunks":null,"duration_ms":2}`,
		"negative-duration": `{"skipped":[],"count":0,"total":0,"chunks":0,"duration_ms":-1}`,
	} {
		if _, err := indexReply(commandRow{Exit: 1, Stdout: text}); err == nil {
			t.Fatal(name, "accepted")
		}
	}
	f := makeFixture(t)
	f.binary = wrappedTarget(t, buildTarget(t), "index-chunks")
	if code, out := runTest(t, f, append(args(f, filepath.Join(f.root, "state")), "--measure")...); code != 1 || !strings.Contains(out, "chunk count differs") {
		t.Fatal("reported chunks not compared with stored index", code, out)
	}
}
func TestEvaluationProtectedRoots(t *testing.T) {
	f := makeFixture(t)
	t.Setenv("HOME", f.root)
	f.binary = filepath.Join(f.root, "missing-tool")
	protected := filepath.Join(f.root, "Library", "Application Support", "Brain")
	if err := os.MkdirAll(protected, 0700); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(protected, "inside")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(protected, "secret.md"), []byte("protected marker"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(f.root, "linked")
	if err := os.Symlink(protected, link); err != nil {
		t.Fatal(err)
	}
	nestedRepo := filepath.Join(f.repo, ".local", "worktrees", "builder")
	if _, err := checkedPath(filepath.Join(protected, "README.md"), nestedRepo); err == nil {
		t.Fatal("nested checkout missed protected state")
	}
	originalRead, originalScan, originalOpen := readControlSource, scanCorpus, readBinarySource
	defer func() { readControlSource, scanCorpus, readBinarySource = originalRead, originalScan, originalOpen }()
	protectedReads, scans, launches := 0, 0, 0
	readControlSource = func(name string, limit int64) ([]byte, error) {
		if containsPath(protected, name) {
			protectedReads++
		}
		return originalRead(name, limit)
	}
	scanCorpus = func(c corpus.Config) (map[string]corpus.Entry, []corpus.Skipped, error) {
		scans++
		return originalScan(c)
	}
	readBinarySource = func(name string, limit int64) ([]byte, error) { launches++; return originalOpen(name, limit) }
	base := corpus.Config{Options: corpus.Options{MaxFileBytes: 1048576, InlineRefreshLimit: 200, PurgeAfterDays: 7}}
	for name, source := range map[string]string{"equal": protected, "parent": f.root, "child": child, "linked": link} {
		base.Sources = []corpus.Source{{Path: source, Project: "test", Literal: true}}
		writeTestJSON(t, f.config, base)
		state := filepath.Join(f.root, "state-"+name)
		if code, _ := runTest(t, f, append(args(f, state), "--measure")...); code != 2 {
			t.Fatalf("%s protected source accepted: %d", name, code)
		}
		if _, err := os.Stat(state); !os.IsNotExist(err) {
			t.Fatalf("%s state created", name)
		}
		if _, err := os.Stat(filepath.Join(f.repo, ".local", "evaluation")); !os.IsNotExist(err) {
			t.Fatalf("%s scratch created", name)
		}
	}
	base.Sources = []corpus.Source{{Path: f.source, Project: "alpha", Literal: true}}
	writeTestJSON(t, f.config, base)
	f.binary = filepath.Join(protected, "tool")
	if err := os.WriteFile(f.binary, []byte("protected"), 0700); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(f.root, "state-binary")
	if code, _ := runTest(t, f, append(args(f, state), "--measure")...); code != 2 {
		t.Fatal("protected binary accepted", code)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("protected binary created state")
	}
	if protectedReads != 0 || scans != 0 || launches != 0 {
		t.Fatal("protected input read, scanned, or launched", protectedReads, scans, launches)
	}
	readControlSource, scanCorpus, readBinarySource = originalRead, originalScan, originalOpen
	f.binary = buildTarget(t)
	if code, out := runTest(t, f, append(args(f, filepath.Join(f.root, "state-control")), "--measure")...); code != 1 || !strings.Contains(out, "results: 30/30") {
		t.Fatal("allowed sibling control", code, out)
	}
}

func TestEvaluationEvidencePaths(t *testing.T) {
	for _, variant := range []string{"linked-evidence", "linked-claim"} {
		t.Run(variant, func(t *testing.T) {
			f := makeFixture(t)
			t.Setenv("HOME", f.root)
			f.binary = filepath.Join(f.root, "missing-tool")
			protected := filepath.Join(f.root, "Library", "Application Support", "Brain")
			if err := os.MkdirAll(protected, 0700); err != nil {
				t.Fatal(err)
			}
			oldRead := readControlSource
			protectedReads := 0
			readControlSource = func(path string, limit int64) ([]byte, error) {
				if containsPath(protected, path) {
					protectedReads++
				}
				return oldRead(path, limit)
			}
			defer func() { readControlSource = oldRead }()
			if variant == "linked-evidence" {
				if err := os.Symlink(protected, filepath.Join(f.repo, ".local")); err != nil {
					t.Fatal(err)
				}
			} else {
				claims := filepath.Join(f.repo, ".local", "evaluation", "claims")
				if err := os.MkdirAll(claims, 0700); err != nil {
					t.Fatal(err)
				}
				keyBytes, _ := os.ReadFile(f.keywords)
				secret := filepath.Join(protected, "private-claim.json")
				writeTestJSON(t, secret, claim{Status: "complete", KeywordSHA256: digest(keyBytes), ResultPath: "private"})
				if err := os.Symlink(secret, filepath.Join(claims, digest(keyBytes)+".json")); err != nil {
					t.Fatal(err)
				}
			}
			state := filepath.Join(f.root, "state")
			if code, _ := runTest(t, f, append(args(f, state), "--measure")...); code != 2 {
				t.Fatal("linked evidence accepted", code)
			}
			if _, err := os.Stat(state); !os.IsNotExist(err) {
				t.Fatal("linked evidence created state")
			}
			if protectedReads != 0 {
				t.Fatal("protected claim contents read", protectedReads)
			}
			if _, err := os.Stat(filepath.Join(protected, "evaluation")); !os.IsNotExist(err) {
				t.Fatal("protected evidence written")
			}
		})
	}
}
func TestEvaluationBinaryBinding(t *testing.T) {
	f := makeFixture(t)
	t.Setenv("HOME", f.root)
	scratch := filepath.Join(f.root, "scratch")
	if err := os.Mkdir(scratch, 0700); err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join(f.root, "Library", "Application Support", "Brain")
	if err := os.MkdirAll(protected, 0700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(protected, "tool")
	if err := os.WriteFile(secret, []byte("secret"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureBinary(secret, f.repo, scratch); err == nil {
		t.Fatal("protected binary read")
	}
	link := filepath.Join(f.root, "linked-tool")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureBinary(link, f.repo, scratch); err == nil {
		t.Fatal("linked binary read")
	}
	if entries, _ := os.ReadDir(scratch); len(entries) != 0 {
		t.Fatal("protected binary created scratch copy")
	}
	oversized := filepath.Join(f.root, "oversized-tool")
	file, err := os.OpenFile(oversized, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Truncate(maxCapturedBinaryBytes + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	oldRead := readBinarySource
	defer func() { readBinarySource = oldRead }()
	reads := 0
	readBinarySource = func(name string, limit int64) ([]byte, error) { reads++; return oldRead(name, limit) }
	if _, _, err = captureBinary(oversized, f.repo, scratch); err == nil || reads != 0 {
		t.Fatal("oversized executable was read", err, reads)
	}
	readBinarySource = oldRead
	permitted := filepath.Join(f.root, "tool")
	if err := os.WriteFile(permitted, []byte("first"), 0700); err != nil {
		t.Fatal(err)
	}
	captured, hash, err := captureBinary(permitted, f.repo, scratch)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(permitted, []byte("second"), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(captured)
	if string(data) != "first" || hash != digest(data) {
		t.Fatal("captured executable changed")
	}
}
func TestEvaluationFloor(t *testing.T) {
	f := makeFixture(t)
	f.binary = buildTarget(t)
	for _, name := range []string{"A.md", "B.md", "C.md"} {
		if err := os.WriteFile(filepath.Join(f.source, name), []byte(strings.Repeat("harbor guide ", 60)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if code, out := runTest(t, f, append(args(f, filepath.Join(f.root, "state-first")), "--measure")...); code != 1 || !strings.Contains(out, "results:") {
		t.Fatal(code, out)
	}
	first := firstResult(t, f)
	before, _, err := readRecord(first, f.repo)
	if err != nil {
		t.Fatal(err)
	}
	if before.HitCount >= 30 {
		t.Fatal("crowded fixture did not lower the first valid score", before.HitCount)
	}
	if err := promote(first, f.floor, f.repo); err != nil {
		t.Fatal(err)
	}
	if err := promote(first, f.floor, f.repo); err != nil {
		t.Fatal("repeat promotion", err)
	}
	floorBytes, err := os.ReadFile(f.floor)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"A.md", "B.md", "C.md"} {
		if err := os.Remove(filepath.Join(f.source, name)); err != nil {
			t.Fatal(err)
		}
	}
	if code, out := runTest(t, f, args(f, filepath.Join(f.root, "state-higher"))...); code != 0 || !strings.Contains(out, "results: 30/30") {
		t.Fatal("higher valid comparison", code, out)
	}
	paths, err := filepath.Glob(filepath.Join(f.repo, ".local", "evaluation", "*-*", "result.json"))
	if err != nil || len(paths) != 2 {
		t.Fatal(paths, err)
	}
	higher := paths[0]
	if higher == first {
		higher = paths[1]
	}
	after, _, err := readRecord(higher, f.repo)
	if err != nil || after.HitCount <= before.HitCount {
		t.Fatal("later record is not a valid higher result", after.HitCount, err)
	}
	if err := promote(higher, f.floor, f.repo); err == nil {
		t.Fatal("later higher result replaced first floor")
	}
	still, err := os.ReadFile(f.floor)
	if err != nil || !bytes.Equal(still, floorBytes) {
		t.Fatal("first floor bytes changed", err)
	}
	keywordData, _ := os.ReadFile(f.keywords)
	sentenceData, _ := os.ReadFile(f.sentences)
	if err := os.Remove(filepath.Join(f.repo, ".local", "evaluation", "claims", digest(keywordData)+".json")); err != nil {
		t.Fatal(err)
	}
	if floor, err := checkFloor(f.floor, f.repo, digest(keywordData), digest(sentenceData)); err != nil || floor != before.HitCount {
		t.Fatal("fresh clone floor", floor, err)
	}
	claimPath := filepath.Join(f.repo, ".local", "evaluation", "claims", digest(keywordData)+".json")
	if err := os.WriteFile(claimPath, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := checkFloor(f.floor, f.repo, digest(keywordData), digest(sentenceData)); err == nil {
		t.Fatal("present conflicting claim accepted")
	}
}
func TestEvaluationRecovery(t *testing.T) {
	f := makeFixture(t)
	claims := filepath.Join(f.repo, ".local", "evaluation", "claims")
	if err := os.MkdirAll(claims, 0700); err != nil {
		t.Fatal(err)
	}
	claimPath := filepath.Join(claims, "key.json")
	attempt := filepath.Join(f.repo, ".local", "evaluation", "attempt")
	if err := os.MkdirAll(attempt, 0700); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, filepath.Join(attempt, "attempt-owner.json"), attemptOwner{KeywordSHA256: "key"})
	result := filepath.Join(attempt, "result.json")
	writeTestJSON(t, claimPath, claim{Status: "running", KeywordSHA256: "key", ResultPath: result})
	if err := recoverClaim(claimPath, f.repo); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(claimPath)
	var got claim
	json.Unmarshal(data, &got)
	if got.Status != "invalid" {
		t.Fatal("interrupted attempt not invalidated")
	}
	// An exclusive claim lets one of two concurrent attempts win.
	path := filepath.Join(claims, "race.json")
	var wg sync.WaitGroup
	wg.Add(2)
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			results <- writeExclusive(path, claim{Status: "running", KeywordSHA256: "race"})
		}()
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatal("concurrent claim winners", wins)
	}
	f.binary = filepath.Join(f.root, "missing-tool")
	argv := append(args(f, filepath.Join(f.root, "state-one")), "--measure")
	if code, _ := runTest(t, f, argv...); code != 1 {
		t.Fatal("failed first attempt", code)
	}
	data, _ = os.ReadFile(f.keywords)
	key := digest(data)
	interrupted := filepath.Join(claims, key+".json")
	if code, out := runTest(t, f, "--recover-measurement", interrupted); code != 0 {
		t.Fatal("recovery", code, out)
	}
	f.binary = buildTarget(t)
	if code, out := runTest(t, f, append(args(f, filepath.Join(f.root, "state-two")), "--measure")...); code != 1 || !strings.Contains(out, "results: 30/30") {
		t.Fatal("retry after invalid claim", code, out)
	}
	paths, err := filepath.Glob(filepath.Join(f.repo, ".local", "evaluation", "*-*", "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	valid := ""
	for _, path := range paths {
		if _, _, err := readRecord(path, f.repo); err == nil {
			valid = path
		}
	}
	if valid == "" {
		t.Fatal("completed result not retained")
	}
	writeTestJSON(t, interrupted, claim{Status: "running", KeywordSHA256: key, ResultPath: valid})
	oldRead := readControlSource
	defer func() { readControlSource = oldRead }()
	readControlSource = func(path string, limit int64) ([]byte, error) {
		if path == valid {
			return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrPermission}
		}
		return oldRead(path, limit)
	}
	if code, _ := runTest(t, f, "--recover-measurement", interrupted); code != 1 {
		t.Fatal("transient read released first claim", code)
	}
	readControlSource = oldRead
	data, err = os.ReadFile(interrupted)
	if err != nil {
		t.Fatal(err)
	}
	var pending claim
	if err = json.Unmarshal(data, &pending); err != nil || pending.Status != "running" {
		t.Fatal("transient read changed claim", pending, err)
	}
	if code, out := runTest(t, f, "--recover-measurement", interrupted); code != 0 {
		t.Fatal("completed result recovery", code, out)
	}
	data, err = os.ReadFile(interrupted)
	if err != nil {
		t.Fatal(err)
	}
	var complete claim
	if err = json.Unmarshal(data, &complete); err != nil || complete.Status != "complete" || complete.ResultPath != valid {
		t.Fatal("first result not preserved", complete, err)
	}
	if code, out := runTest(t, f, "--promote-floor", valid, "--floor", f.floor); code != 0 {
		t.Fatal("recovered first result promotion", code, out)
	}
	after, err := filepath.Glob(filepath.Join(f.repo, ".local", "evaluation", "*-*", "result.json"))
	if err != nil || len(after) != len(paths) {
		t.Fatal("maintenance launched another measurement", len(after), len(paths), err)
	}
}

func TestEvaluationRecoveryKeepsFirstWhenCaptureMissing(t *testing.T) {
	f := makeFixture(t)
	f.binary = buildTarget(t)
	for _, name := range []string{"A.md", "B.md", "C.md"} {
		if err := os.WriteFile(filepath.Join(f.source, name), []byte(strings.Repeat("harbor guide ", 60)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if code, out := runTest(t, f, append(args(f, filepath.Join(f.root, "state")), "--measure")...); code != 1 || !strings.Contains(out, "results: 0/30") {
		t.Fatal("first measurement", code, out)
	}
	result := firstResult(t, f)
	record, _, err := readRecord(result, f.repo)
	if err != nil {
		t.Fatal(err)
	}
	claimPath := filepath.Join(f.repo, ".local", "evaluation", "claims", record.KeywordSHA256+".json")
	writeTestJSON(t, claimPath, claim{Status: "running", KeywordSHA256: record.KeywordSHA256, ResultPath: result})
	missing := record.CapturedBinary + ".temporarily-missing"
	if err := os.Rename(record.CapturedBinary, missing); err != nil {
		t.Fatal(err)
	}
	if code, _ := runTest(t, f, "--recover-measurement", claimPath); code != 1 {
		t.Fatal("recovery accepted missing capture", code)
	}
	data, err := os.ReadFile(claimPath)
	if err != nil {
		t.Fatal(err)
	}
	var pending claim
	if err := json.Unmarshal(data, &pending); err != nil || pending.Status != "running" {
		t.Fatal("first claim was released", pending, err)
	}
	if err := os.Rename(missing, record.CapturedBinary); err != nil {
		t.Fatal(err)
	}
	if code, out := runTest(t, f, "--recover-measurement", claimPath); code != 0 {
		t.Fatal("original result did not recover", code, out)
	}
	for _, name := range []string{"A.md", "B.md", "C.md"} {
		if err := os.Remove(filepath.Join(f.source, name)); err != nil {
			t.Fatal(err)
		}
	}
	if code, out := runTest(t, f, append(args(f, filepath.Join(f.root, "state-later")), "--measure")...); code != 1 || !strings.Contains(out, "first measurement already claimed") {
		t.Fatal("later measurement replaced first", code, out)
	}
}

func TestEvaluationConfigPreservesUnknownSourceFields(t *testing.T) {
	f := makeFixture(t)
	f.binary = buildTarget(t)
	data, err := os.ReadFile(f.config)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	raw["future_top_level"] = "retained"
	first := raw["sources"].([]any)[0].(map[string]any)
	first["future_source_field"] = "retain this value"
	writeTestJSON(t, f.config, raw)
	if code, out := runTest(t, f, append(args(f, filepath.Join(f.root, "state")), "--measure")...); code != 1 || !strings.Contains(out, "results: 30/30") {
		t.Fatal("measurement", code, out)
	}
	record, _, err := readRecord(firstResult(t, f), f.repo)
	if err != nil {
		t.Fatal(err)
	}
	var effective map[string]any
	if err := json.Unmarshal(record.EffectiveConfigSnapshot, &effective); err != nil {
		t.Fatal(err)
	}
	if effective["future_top_level"] != "retained" || effective["sources"].([]any)[0].(map[string]any)["future_source_field"] != "retain this value" {
		t.Fatal("effective config lost future fields")
	}
}

func TestEvaluationRecoveryRejectsFake(t *testing.T) {
	f := makeFixture(t)
	attempt := filepath.Join(f.repo, ".local", "evaluation", "fake-attempt")
	claims := filepath.Join(f.repo, ".local", "evaluation", "claims")
	for _, dir := range []string{attempt, claims} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeTestJSON(t, filepath.Join(attempt, "attempt-owner.json"), attemptOwner{KeywordSHA256: "fake"})
	broken := resultRecord{Version: scoringVersion, Status: "complete", KeywordSHA256: "fake", SentenceSHA256: "sentence", BinarySHA256: "binary", ManifestSHA256: "manifest", HitCount: 30, Rows: make([]resultRow, 30)}
	for i := range broken.Rows {
		hit := true
		broken.Rows[i] = resultRow{ID: "duplicate", Status: "complete", Hit: &hit}
	}
	result := filepath.Join(attempt, "result.json")
	writeTestJSON(t, result, broken)
	claimPath := filepath.Join(claims, "fake.json")
	writeTestJSON(t, claimPath, claim{Status: "running", KeywordSHA256: "fake", ResultPath: result})
	if code, _ := runTest(t, f, "--recover-measurement", claimPath); code != 0 {
		t.Fatal("invalid evidence should become an invalid claim", code)
	}
	data, err := os.ReadFile(claimPath)
	if err != nil {
		t.Fatal(err)
	}
	var got claim
	if err = json.Unmarshal(data, &got); err != nil || got.Status != "invalid" {
		t.Fatal(got, err)
	}
	if code, _ := runTest(t, f, "--promote-floor", result, "--floor", f.floor); code != 1 {
		t.Fatal("fake 30/30 was promoted", code)
	}
	if _, err := os.Stat(f.floor); !os.IsNotExist(err) {
		t.Fatal("fake floor file exists", err)
	}
}

func TestEvaluationPrivateSourceKind(t *testing.T) {
	f := makeFixture(t)
	hidden := filepath.Join(f.source, ".local")
	if err := os.Mkdir(hidden, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hidden, "secret.md"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	config := corpus.Config{Sources: []corpus.Source{{Path: hidden, Kind: "note", Literal: true}}, Options: corpus.Options{MaxFileBytes: 1048576, InlineRefreshLimit: 200, PurgeAfterDays: 7}}
	writeTestJSON(t, f.config, config)
	if _, _, _, _, err := loadConfig(f.config, f.repo); err == nil {
		t.Fatal("caller note kind entered excluded source")
	}
	f.binary = filepath.Join(f.root, "missing-tool")
	previous := scanCorpus
	scans := 0
	scanCorpus = func(c corpus.Config) (map[string]corpus.Entry, []corpus.Skipped, error) {
		scans++
		return previous(c)
	}
	defer func() { scanCorpus = previous }()
	state := filepath.Join(f.root, "private-source-state")
	if code, _ := runTest(t, f, append(args(f, state), "--measure")...); code != 2 || scans != 0 {
		t.Fatal("private note-kind source reached scan", code, scans)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("private source created state")
	}
}

func TestEvaluationIsolatedCommandEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix script fixture; Windows execution is checked on its native host")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "show-env")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' \"$HOME\" \"$BRAIN_HOME\" \"$PATH\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(root, "inherited-home"))
	t.Setenv("BRAIN_HOME", filepath.Join(root, "inherited-state"))
	isolatedHome := filepath.Join(root, "isolated-home")
	isolatedState := filepath.Join(root, "isolated-state")
	d := driver{binary: binary, home: isolatedHome, state: isolatedState}
	row := d.command(context.Background(), "ignored")
	if row.Exit != 0 || row.Stdout != isolatedHome+"\n"+isolatedState+"\n\n" {
		t.Fatal("inherited environment reached child", row.Exit, row.Stdout, row.Stderr)
	}
}

func TestEvaluationCompactRanks(t *testing.T) {
	f := makeFixture(t)
	f.binary = buildTarget(t)
	for i := 0; i < 120; i++ {
		name := filepath.Join(f.source, fmt.Sprintf("extra-%03d.md", i))
		if err := os.WriteFile(name, []byte("# Extra\n\nharbor guide "+strings.Repeat("long snippet marker ", 35)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if code, out := runTest(t, f, append(args(f, filepath.Join(f.root, "state-many")), "--measure")...); code != 1 || !strings.Contains(out, "results:") {
		t.Fatal(code, out)
	}
	path := firstResult(t, f)
	record, _, err := readRecord(path, f.repo)
	if err != nil {
		t.Fatal("many-rank evidence cannot recover", err)
	}
	if len(record.Rows[0].Ranked) <= 100 {
		t.Fatal("fixture did not exercise ranks beyond public limit", len(record.Rows[0].Ranked))
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() > 10<<20 {
		t.Fatal("rank evidence size", info, err)
	}
	trace, _ := json.Marshal(record.Rows[0].Ranked)
	if bytes.Contains(trace, []byte("long snippet marker")) {
		t.Fatal("adapter snippets entered complete rank trace")
	}
}

func TestEvaluationEvidenceIntegrity(t *testing.T) {
	f := makeFixture(t)
	f.binary = buildTarget(t)
	if code, out := runTest(t, f, append(args(f, filepath.Join(f.root, "state-evidence")), "--measure")...); code != 1 || !strings.Contains(out, "results: 30/30") {
		t.Fatal(code, out)
	}
	path := firstResult(t, f)
	valid, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var original resultRecord
	if err := json.Unmarshal(valid, &original); err != nil {
		t.Fatal(err)
	}
	changes := map[string]func(*resultRecord){
		"question digest":  func(r *resultRecord) { r.KeywordSHA256 = strings.Repeat("0", 64) },
		"question schema":  func(r *resultRecord) { r.KeywordSnapshot = []byte(`{}`); r.KeywordSHA256 = digest(r.KeywordSnapshot) },
		"config digest":    func(r *resultRecord) { r.EffectiveConfigSHA256 = strings.Repeat("0", 64) },
		"manifest digest":  func(r *resultRecord) { r.ManifestSHA256 = strings.Repeat("0", 64) },
		"binary digest":    func(r *resultRecord) { r.BinarySHA256 = strings.Repeat("0", 64) },
		"adapter identity": func(r *resultRecord) { r.AdapterSHA256 = "" },
		"duplicate file": func(r *resultRecord) {
			r.Files = append(r.Files, r.Files[0])
			data, _ := json.Marshal(r.Files)
			r.ManifestSHA256 = digest(data)
		},
		"outcome digest": func(r *resultRecord) { r.Outcomes[0].SHA256 = strings.Repeat("0", 64) },
		"owner digest": func(r *resultRecord) {
			var q questionSet
			_ = json.Unmarshal(r.KeywordSnapshot, &q)
			q.Entries[0].Evidence[0].SHA256 = strings.Repeat("0", 64)
			r.KeywordSnapshot, _ = json.Marshal(q)
			r.KeywordSHA256 = digest(r.KeywordSnapshot)
		},
		"missing command":      func(r *resultRecord) { r.Commands = r.Commands[:30] },
		"index chunks":         func(r *resultRecord) { r.IndexChunks++ },
		"row order":            func(r *resultRecord) { r.Rows[0], r.Rows[1] = r.Rows[1], r.Rows[0] },
		"rank order":           func(r *resultRecord) { r.Rows[0].Ranked[0].Rank++ },
		"unknown alias":        func(r *resultRecord) { r.Rows[0].Ranked[0].AlsoIn = append(r.Rows[0].Ranked[0].AlsoIn, "/unknown.md") },
		"public prefix":        func(r *resultRecord) { r.Commands[1].Stdout = `{"count":0,"total":0,"results":[]}` },
		"hit decision":         func(r *resultRecord) { *r.Rows[0].Hit = !*r.Rows[0].Hit },
		"captured binary path": func(r *resultRecord) { r.CapturedBinary = filepath.Join(f.root, "other") },
		"unowned outcome":      func(r *resultRecord) { r.Outcomes[0].Path = "other.md" },
		"missing outcome":      func(r *resultRecord) { r.Outcomes = nil },
		"question identity":    func(r *resultRecord) { r.Rows[0].ID = "K99" },
		"search argument":      func(r *resultRecord) { r.Commands[1].Argv[2] = "different" },
		"index argument":       func(r *resultRecord) { r.Commands[0].Argv[1] = "search" },
		"rank identity":        func(r *resultRecord) { r.Rows[0].Ranked[0].ID = "" },
		"rank count":           func(r *resultRecord) { r.Rows[0].Total++ },
		"hit count":            func(r *resultRecord) { r.HitCount-- },
	}
	for name, mutate := range changes {
		t.Run(name, func(t *testing.T) {
			r := original
			data, _ := json.Marshal(original)
			if err := json.Unmarshal(data, &r); err != nil {
				t.Fatal(err)
			}
			mutate(&r)
			writeTestJSON(t, path, r)
			if _, _, err := readRecord(path, f.repo); !errors.Is(err, errInvalidEvidence) {
				t.Fatalf("accepted damaged %s evidence: %v", name, err)
			}
			if err := os.WriteFile(path, valid, 0600); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, _, err := readRecord(path, f.repo); err != nil {
		t.Fatal("valid evidence failed after controls", err)
	}
}

func TestEvaluationRunRefusals(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(*testing.T, *fixture, string) []string
		want    int
	}{
		{"help", func(_ *testing.T, _ *fixture, _ string) []string { return []string{"--help"} }, 0},
		{"unknown flag", func(_ *testing.T, _ *fixture, _ string) []string { return []string{"--missing"} }, 2},
		{"positional argument", func(_ *testing.T, _ *fixture, _ string) []string { return []string{"extra"} }, 2},
		{"required options", func(_ *testing.T, _ *fixture, _ string) []string { return nil }, 2},
		{"conflicting maintenance", func(_ *testing.T, _ *fixture, _ string) []string {
			return []string{"--promote-floor", "x", "--recover-measurement", "y"}
		}, 2},
		{"measurement with recovery", func(_ *testing.T, _ *fixture, _ string) []string {
			return []string{"--recover-measurement", "x", "--measure"}
		}, 2},
		{"protected promotion", func(_ *testing.T, f *fixture, _ string) []string {
			return []string{"--promote-floor", filepath.Join(protectedPaths(f.repo)[0], "result.json"), "--floor", f.floor}
		}, 2},
		{"protected floor", func(_ *testing.T, f *fixture, _ string) []string {
			return []string{"--promote-floor", f.config, "--floor", filepath.Join(protectedPaths(f.repo)[0], "floor.json")}
		}, 2},
		{"existing state", func(t *testing.T, f *fixture, state string) []string {
			if err := os.Mkdir(state, 0700); err != nil {
				t.Fatal(err)
			}
			return append(args(*f, state), "--measure")
		}, 2},
		{"missing floor", func(_ *testing.T, f *fixture, state string) []string { return args(*f, state) }, 2},
		{"source outside root", func(_ *testing.T, f *fixture, state string) []string {
			a := append(args(*f, state), "--measure")
			a[len(a)-1] = f.repo
			return a
		}, 2},
		{"source and state overlap", func(_ *testing.T, f *fixture, _ string) []string {
			return append(args(*f, filepath.Join(f.source, "state")), "--measure")
		}, 2},
		{"linked corpus root", func(t *testing.T, f *fixture, state string) []string {
			link := filepath.Join(f.root, "linked")
			if err := os.Symlink(f.root, link); err != nil {
				t.Skip(err)
			}
			a := append(args(*f, state), "--measure")
			a[len(a)-1] = link
			return a
		}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := makeFixture(t)
			state := filepath.Join(f.root, "state")
			argv := tc.prepare(t, &f, state)
			code, output := runTest(t, f, argv...)
			if code != tc.want {
				t.Fatalf("refusal exit = %d, want %d: %s", code, tc.want, output)
			}
		})
	}
}

func TestEvaluationSourceDriftRefusesCompletion(t *testing.T) {
	f := makeFixture(t)
	f.binary = buildTarget(t)
	path := filepath.Join(f.source, "README.md")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	previous := readCorpusSource
	defer func() { readCorpusSource = previous }()
	reads := 0
	readCorpusSource = func(name string, limit int64) ([]byte, error) {
		if name == path {
			reads++
			if reads == 2 {
				if err := os.WriteFile(path, []byte("# Replaced harbor guide\n\nThe changed source."), 0600); err != nil {
					return nil, err
				}
			}
		}
		return previous(name, limit)
	}
	code, output := runTest(t, f, append(args(f, filepath.Join(f.root, "state-drift")), "--measure")...)
	if code != 1 || !strings.Contains(output, "corpus changed during measurement") || reads < 2 {
		t.Fatalf("source drift exit=%d reads=%d output=%s", code, reads, output)
	}
	var record resultRecord
	data, err := os.ReadFile(firstResult(t, f))
	if err != nil || json.Unmarshal(data, &record) != nil || record.Status == "complete" {
		t.Fatal("source drift produced complete evidence", err)
	}
	readCorpusSource = previous
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	code, output = runTest(t, f, append(args(f, filepath.Join(f.root, "state-retry")), "--measure")...)
	if code != 1 || !strings.Contains(output, "first measurement already claimed") {
		t.Fatalf("running claim was reused: %d %s", code, output)
	}
}

func TestEvaluationFrozenInputDriftRefusesCompletion(t *testing.T) {
	f := makeFixture(t)
	f.binary = buildTarget(t)
	previous := readControlSource
	defer func() { readControlSource = previous }()
	reads := 0
	readControlSource = func(path string, limit int64) ([]byte, error) {
		if path == f.config {
			reads++
			if reads == 3 {
				if err := os.WriteFile(path, []byte(`{"sources":[]}`), 0600); err != nil {
					return nil, err
				}
			}
		}
		return previous(path, limit)
	}
	code, output := runTest(t, f, append(args(f, filepath.Join(f.root, "state-input-drift")), "--measure")...)
	if code != 1 || !strings.Contains(output, "frozen input changed during measurement") || reads < 3 {
		t.Fatalf("input drift exit=%d reads=%d output=%s", code, reads, output)
	}
}

func TestEvaluationRecoveryRefusals(t *testing.T) {
	f := makeFixture(t)
	base := filepath.Join(f.repo, ".local", "evaluation")
	claims := filepath.Join(base, "claims")
	attempt := filepath.Join(base, "attempt")
	for _, dir := range []string{claims, attempt} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	result := filepath.Join(attempt, "result.json")
	owner := filepath.Join(attempt, "attempt-owner.json")
	path := filepath.Join(claims, "key.json")
	for _, tc := range []struct {
		name  string
		setup func(*testing.T)
	}{
		{"claim outside folder", func(t *testing.T) {
			writeTestJSON(t, filepath.Join(base, "key.json"), claim{Status: "running", KeywordSHA256: "key", ResultPath: result})
		}},
		{"invalid claim JSON", func(t *testing.T) {
			if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing claim identity", func(t *testing.T) { writeTestJSON(t, path, claim{Status: "running", ResultPath: result}) }},
		{"claim name mismatch", func(t *testing.T) {
			writeTestJSON(t, path, claim{Status: "running", KeywordSHA256: "other", ResultPath: result})
		}},
		{"result outside attempt", func(t *testing.T) {
			writeTestJSON(t, path, claim{Status: "running", KeywordSHA256: "key", ResultPath: filepath.Join(f.root, "other.json")})
		}},
		{"missing owner marker", func(t *testing.T) {
			writeTestJSON(t, path, claim{Status: "running", KeywordSHA256: "key", ResultPath: result})
		}},
		{"bad owner marker", func(t *testing.T) {
			writeTestJSON(t, path, claim{Status: "running", KeywordSHA256: "key", ResultPath: result})
			writeTestJSON(t, owner, attemptOwner{KeywordSHA256: "other"})
		}},
		{"unknown claim status", func(t *testing.T) {
			writeTestJSON(t, path, claim{Status: "unknown", KeywordSHA256: "key", ResultPath: result})
			writeTestJSON(t, owner, attemptOwner{KeywordSHA256: "key"})
		}},
		{"complete without result", func(t *testing.T) {
			writeTestJSON(t, path, claim{Status: "complete", KeywordSHA256: "key", ResultPath: result})
			writeTestJSON(t, owner, attemptOwner{KeywordSHA256: "key"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Remove(path)
			_ = os.Remove(owner)
			tc.setup(t)
			candidate := path
			if tc.name == "claim outside folder" {
				candidate = filepath.Join(base, "key.json")
			}
			if err := recoverClaim(candidate, f.repo); err == nil {
				t.Fatal("unsafe claim accepted")
			}
		})
	}
}
