package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigCaseAliasesRefuse(t *testing.T) {
	j := newJourney(t)
	if err := os.Mkdir(j.state, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(j.base, "selected.json")
	source := map[string]any{"path": j.source, "source_id": 0, "project": "fixture", "kind": "doc", "literal": true}
	options := map[string]any{"max_file_bytes": 1048576, "inline_refresh_limit": 200, "purge_after_days": 7}
	baseline := map[string]any{"sources": []any{source}, "next_source_id": 1, "options": options, "exclude": []any{}, "third_party": []any{}, "include": []any{}}
	canonical, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	inputs := map[string][]byte{}
	for _, scope := range []string{"config", "source", "options"} {
		fields := baseline
		if scope == "source" {
			fields = source
		}
		if scope == "options" {
			fields = options
		}
		for name := range fields {
			for _, paired := range []bool{false, true} {
				var document map[string]any
				if err := json.Unmarshal(canonical, &document); err != nil {
					t.Fatal(err)
				}
				target := document
				if scope == "source" {
					target = document["sources"].([]any)[0].(map[string]any)
				}
				if scope == "options" {
					target = document["options"].(map[string]any)
				}
				target[strings.ToUpper(name)] = target[name]
				label := scope + "/" + name + "/alone"
				if paired {
					label = scope + "/" + name + "/paired"
				} else {
					delete(target, name)
				}
				data, err := json.Marshal(document)
				if err != nil {
					t.Fatal(err)
				}
				inputs[label] = data
			}
		}
	}
	inputs["source-array-larger"] = []byte(`{"sources":[],"Sources":[{"path":` + string(mustJSON(t, j.source)) + `}],"options":` + string(mustJSON(t, options)) + `}`)
	inputs["source-array-different"] = []byte(`{"sources":[{"path":` + string(mustJSON(t, j.source)) + `}],"Sources":[{"path":` + string(mustJSON(t, j.base)) + `}],"options":` + string(mustJSON(t, options)) + `}`)
	inputs["source-id-mixed"] = bytes.Replace(canonical, []byte(`"source_id":0`), []byte(`"source_id":0,"Source_ID":0`), 1)
	inputs["unicode-known-alias"] = bytes.Replace(canonical, []byte(`"sources"`), []byte(`"ſources"`), 1)
	for name, input := range inputs {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, input, 0600); err != nil {
				t.Fatal(err)
			}
			for _, verb := range [][]string{{"sources", "list"}, {"index"}, {"exclude", "add", "draft"}} {
				args := append(append([]string{}, verb...), "--config", path, "--state-dir", j.state, "--format", "json")
				command := exec.Command(testBinary, args...)
				command.Env, command.Dir = j.environment, j.base
				var out, stderr bytes.Buffer
				command.Stdout, command.Stderr = &out, &stderr
				err := command.Run()
				exit, ok := err.(*exec.ExitError)
				if !ok || exit.ExitCode() != 1 || stderr.Len() != 0 {
					t.Fatalf("%v: want structured exit 1 without stderr, got %v; stdout=%q stderr=%q", verb, err, out.String(), stderr.String())
				}
				var reply struct {
					Error struct {
						Code, Message string
						NextArgv      [][]string `json:"next_argv"`
					} `json:"error"`
				}
				decoder := json.NewDecoder(&out)
				if err := decoder.Decode(&reply); err != nil || reply.Error.Code != "config-invalid" || strings.Contains(reply.Error.Message, j.base) {
					t.Fatal(verb, reply, err)
				}
				var extra any
				if decoder.Decode(&extra) != io.EOF {
					t.Fatal("more than one reply")
				}
				if len(reply.Error.NextArgv) == 0 {
					t.Fatal("missing recovery")
				}
				for flag, value := range map[string]string{"--config": path, "--state-dir": j.state} {
					count := 0
					for i, word := range reply.Error.NextArgv[0] {
						if word == flag && i+1 < len(reply.Error.NextArgv[0]) && reply.Error.NextArgv[0][i+1] == value {
							count++
						}
					}
					if count != 1 {
						t.Fatal("lost recovery scope", flag, reply.Error.NextArgv)
					}
				}
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(input, after) {
					t.Fatal("invalid config changed", verb, err)
				}
			}
		})
	}
}
