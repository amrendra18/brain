package main

import (
	corpusrules "brain/internal/corpus"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCorpusOwnerMissing(t *testing.T) {
	j := newJourney(t)
	j.put(t, "project/README.md", "# Present owner")
	if err := corpusOwners(j.source, []string{"project/README.md"}); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"missing/README.md", "project"} {
		if err := corpusOwners(j.source, []string{owner}); err == nil || !strings.Contains(err.Error(), owner) {
			t.Fatalf("missing or non-file owner was not identified: %s: %v", owner, err)
		}
	}
}

func TestRealCorpusSelection(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("BRAIN_TEST_CORPUS", root)
	if got := realCorpus(t); got != root {
		t.Fatal("explicit corpus was not selected", got, root)
	}
	if err := corpusOwners(realCorpus(t), []string{"missing/README.md"}); err == nil {
		t.Fatal("explicit empty corpus concealed a missing owner")
	}
}

func TestRealJourneySourceBoundary(t *testing.T) {
	fixture := newJourney(t)
	fixture.put(t, "allowed/README.md", "# Allowed document\n\npermittedcanary")
	fixture.put(t, "old_drafts/brain/research.md", "# Archived research\n\nresearchcanary")
	fixture.put(t, "reading/notes.md", "# Allowed learning notes\n\nlearningcanary")
	fixture.put(t, "reading/upstream-copy/README.md", "# Excluded third-party input\n\nthirdpartycanary")
	fixture.put(t, "brain/README.md", "# Protected synthetic project\n\nforbiddencanary")
	blocked := filepath.Join(fixture.source, "brain")
	if err := os.Chmod(blocked, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(blocked, 0700) })
	j := realJourney(t, fixture.source, []string{"upstream-copy"})
	config, err := corpusrules.Load(j.state)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Sources) != 4 {
		t.Fatal("want three permitted source folders plus private notes", config.Sources)
	}
	for _, source := range config.Sources {
		if source.Path != filepath.Join(fixture.source, "allowed") && source.Path != filepath.Join(fixture.source, "old_drafts") && source.Path != filepath.Join(fixture.source, "reading") && source.Path != filepath.Join(j.state, "notes") {
			t.Fatal("unexpected source", source)
		}
	}
	observedBinary := exclusionObservedBinary(t, false)
	code, reply, opened := sourceObservedCommand(t, observedBinary, j, "index")
	if code != 0 || reply["new"] != float64(3) || reply["chunks"].(float64) < 3 || reply["count"] != float64(0) {
		t.Fatal("permitted corpus was not fully indexed without gaps", code, reply)
	}
	allowedOpens, protectedOpens := 0, 0
	for _, path := range opened {
		if path == filepath.Join(fixture.source, "allowed", "README.md") {
			allowedOpens++
		}
		for _, name := range []string{"brain"} {
			root := filepath.Join(fixture.source, name)
			if path == root || strings.HasPrefix(path, root+string(filepath.Separator)) {
				protectedOpens++
			}
		}
	}
	if allowedOpens == 0 || protectedOpens != 0 {
		t.Fatalf("observed %d allowed opens and %d protected opens; want allowed > 0 and protected = 0", allowedOpens, protectedOpens)
	}
	for _, query := range []string{"permittedcanary", "researchcanary", "learningcanary"} {
		if code, reply := j.run(t, "search", query); code != 0 || reply["total"] != float64(1) {
			t.Fatal("permitted source was not searchable", code, reply)
		}
	}
	for _, query := range []string{"forbiddencanary", "thirdpartycanary"} {
		if code, reply := j.run(t, "search", query); code != 0 || reply["total"] != float64(0) {
			t.Fatal("excluded synthetic source entered the index", code, reply)
		}
	}
	// Prove the open observer catches a deliberately selected control file.
	control := newJourney(t)
	control.put(t, "README.md", "# Deliberate observer control\n\ncontrolcanary")
	if code, reply := control.run(t, "init", "--source", control.source); code != 0 {
		t.Fatal(code, reply)
	}
	code, reply, controlOpens := sourceObservedCommand(t, observedBinary, control, "index")
	if code != 0 {
		t.Fatal(code, reply)
	}
	controlOpensCount := 0
	for _, path := range controlOpens {
		if path == filepath.Join(control.source, "README.md") {
			controlOpensCount++
		}
	}
	if controlOpensCount == 0 || exclusionNoOpenProof(allowedOpens, controlOpensCount) == nil {
		t.Fatal("open observer missed or accepted deliberate control access")
	}
	if code, reply := control.run(t, "search", "controlcanary"); code != 0 || reply["total"] != float64(1) {
		t.Fatal("control could not detect deliberate fixture access", code, reply)
	}
}

func TestRealCorpusProtectedRoot(t *testing.T) {
	fixture := newJourney(t)
	fixture.put(t, "brain/nested/project/README.md", "# Owned protected fixture\n\nprotectedrootcanary")
	fixture.put(t, "allowed/project/README.md", "# Allowed sibling\n\nallowedrootcanary")
	var paths []string
	for _, name := range []string{"brain"} {
		protected := filepath.Join(fixture.source, name)
		paths = append(paths, protected, filepath.Join(protected, "nested"))
		if runtime.GOOS != "windows" {
			alias := filepath.Join(fixture.base, name+"-alias")
			if err := os.Symlink(protected, alias); err != nil {
				t.Fatal(err)
			}
			paths = append(paths, alias, filepath.Join(alias, "nested"))
		}
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, selected := range paths {
		for _, entry := range []string{"selection", "journey", "owners"} {
			command := exec.Command(self, "-test.run=^TestRealCorpusProtectedRootProbe$")
			command.Env = append(os.Environ(), "BRAIN_TEST_CORPUS="+selected, "BRAIN_CORPUS_PROBE="+entry)
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "protected corpus root") {
				t.Errorf("%s accepted protected fixture %s: %v\n%s", entry, selected, err, output)
			}
		}
	}
	t.Setenv("BRAIN_TEST_CORPUS", filepath.Join(fixture.source, "allowed"))
	j := realJourney(t, realCorpus(t), nil)
	if code, reply := j.run(t, "index"); code != 0 || reply["new"] != float64(1) {
		t.Fatal("allowed sibling did not index", code, reply)
	}
	if code, reply := j.run(t, "search", "allowedrootcanary"); code != 0 || reply["total"] != float64(1) {
		t.Fatal("allowed sibling was not searchable", code, reply)
	}
}

func TestRealCorpusProtectedRootProbe(t *testing.T) {
	entry := os.Getenv("BRAIN_CORPUS_PROBE")
	if entry == "" {
		t.Skip("subprocess probe for owned protected-root fixtures")
	}
	if entry == "selection" {
		realCorpus(t)
		return
	}
	if entry == "owners" {
		if err := corpusOwners(os.Getenv("BRAIN_TEST_CORPUS"), nil); err != nil {
			t.Fatal(err)
		}
		return
	}
	j := realJourney(t, os.Getenv("BRAIN_TEST_CORPUS"), nil)
	if code, reply := j.run(t, "index"); code != 0 {
		t.Fatal(code, reply)
	}
}
