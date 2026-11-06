package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestIgnoreRefusalRecovery(t *testing.T) {
	root := t.TempDir()
	source, state := filepath.Join(root, "source"), filepath.Join(root, "state")
	config := filepath.Join(root, "selected.json")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("# Harbor\n\nAllowedmarker stays visible.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	invoke := func(args ...string) (int, []byte) {
		t.Helper()
		var out bytes.Buffer
		code := Run(append(args, "--config", config, "--state-dir", state, "--format", "json"), &out)
		return code, out.Bytes()
	}
	if code, data := invoke("init", "--source", source); code != 0 {
		t.Fatal(code, string(data))
	}
	control := filepath.Join(source, ".brainignore")
	if err := os.Mkdir(control, 0700); err != nil {
		t.Fatal(err)
	}
	code, data := invoke("index")
	var result struct {
		Error Failure `json:"error"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if code != 1 || result.Error.Code != "ignore-unreadable" || len(result.Error.NextArgv) != 1 || bytes.Contains([]byte(result.Error.Message), []byte(root)) {
		t.Fatal(code, string(data))
	}
	argv := result.Error.NextArgv[0]
	for flag, want := range map[string]string{"--state-dir": state, "--config": config} {
		found := false
		for i := 1; i+1 < len(argv); i++ {
			if argv[i] == flag && argv[i+1] == want {
				found = true
			}
		}
		if !found {
			t.Fatal("recovery lost scope", flag, argv)
		}
	}
	if err := os.Remove(control); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := Run(argv[1:], &out); code != 0 {
		t.Fatal(code, out.String())
	}
	code, data = invoke("search", "Allowedmarker")
	var search struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(data, &search); err != nil || code != 0 || search.Count != 1 {
		t.Fatal(code, string(data), err)
	}
}
