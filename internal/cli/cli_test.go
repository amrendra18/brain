package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

type closedWriter struct{}

func (closedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestRunVersionAndHelp(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"--help"}, {"--version"}, {"--version", "--format", "json"}, {"--version", "--format=text"}} {
		var output bytes.Buffer
		if Run(args, &output) != 0 || output.Len() == 0 {
			t.Fatalf("%v: %s", args, output.String())
		}
		if Run(args, closedWriter{}) != 1 {
			t.Fatalf("closed output: %v", args)
		}
	}
}

func TestBadInputShapes(t *testing.T) {
	for _, test := range []struct {
		args []string
		code string
	}{
		{[]string{"--unknown"}, "unknown-flag"},
		{[]string{"unknown"}, "unknown-command"},
		{[]string{"--version", "--format"}, "missing-argument"},
		{[]string{"--version", "--format", "xml"}, "invalid-value"},
		{[]string{"--version", "extra"}, "unexpected-argument"},
		{[]string{"help", "--unknown"}, "unknown-flag"},
	} {
		var output bytes.Buffer
		if Run(test.args, &output) != 2 {
			t.Fatal(test.args, output.String())
		}
		var reply refusal
		if err := json.Unmarshal(output.Bytes(), &reply); err != nil {
			t.Fatal(err)
		}
		if reply.Error.Code != test.code || len(reply.Error.Next) != 1 || len(reply.Error.NextArgv) != 1 {
			t.Fatal(reply)
		}
		if Run(test.args, closedWriter{}) != 1 {
			t.Fatal("closed refusal output")
		}
	}
}

func TestASCIIJSON(t *testing.T) {
	var output bytes.Buffer
	want := "é 😀"
	if WriteJSON(&output, want) != 0 {
		t.Fatal("JSON write failed")
	}
	for _, b := range output.Bytes() {
		if b > 127 {
			t.Fatal("non-ASCII output")
		}
	}
	var got string
	if err := json.Unmarshal(output.Bytes(), &got); err != nil || got != want || !strings.HasSuffix(output.String(), "\n") {
		t.Fatal(got, err)
	}
	if WriteJSON(&output, make(chan int)) != 1 {
		t.Fatal("invalid JSON value accepted")
	}
}

func TestRefusalNamesHidePaths(t *testing.T) {
	for _, input := range []string{"/private/example", `C:\private\example`, `\\server\share`, "--bad=/private/example"} {
		name := inputName(input)
		if strings.Contains(name, "private") || strings.Contains(name, "server") {
			t.Fatal(name)
		}
		if strings.HasPrefix(input, "--bad") && !strings.Contains(name, "--bad") {
			t.Fatal(name)
		}
	}
}

func TestUnknownInputListsConfigFlag(t *testing.T) {
	var output bytes.Buffer
	Run([]string{"--unknown"}, &output)
	var reply refusal
	if err := json.Unmarshal(output.Bytes(), &reply); err != nil || !strings.Contains(reply.Error.Message, "--config") {
		t.Fatal("unknown input message omits --config", output.String(), err)
	}
}
