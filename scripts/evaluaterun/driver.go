package evaluaterun

import (
	"brain/internal/index"
	"brain/internal/platform"
	"brain/internal/query"
	"brain/internal/search"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"
)

type driver struct {
	binary, state, home string
	commands            []commandRow
}

var readBinarySource = platform.ReadSource

const maxCapturedBinaryBytes int64 = 64 << 20

func captureBinary(path, repo, scratch string) (string, string, error) {
	checked, err := checkedPath(path, repo)
	if err != nil {
		return "", "", err
	}
	info, err := os.Lstat(checked)
	if err != nil {
		return "", "", err
	}
	if !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode()&0111 == 0 {
		return "", "", errors.New("binary must be an executable regular file")
	}
	if info.Size() > maxCapturedBinaryBytes {
		return "", "", errors.New("binary exceeds the 64 MiB capture limit")
	}
	data, err := readBinarySource(checked, info.Size())
	if err != nil {
		return "", "", err
	}
	name := filepath.Join(scratch, "brain"+filepath.Ext(checked))
	output, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return "", "", err
	}
	_, copyErr := output.Write(data)
	closeErr := output.Close()
	if err = errors.Join(copyErr, closeErr); err != nil {
		return "", "", err
	}
	return name, digest(data), nil
}
func (d *driver) command(ctx context.Context, words ...string) commandRow {
	args := append([]string{}, words...)
	args = append(args, "--state-dir", d.state, "--format", "json")
	row := commandRow{Argv: append([]string{d.binary}, args...), Exit: -1}
	cmd := exec.CommandContext(ctx, d.binary, args...)
	env := []string{}
	for _, pair := range os.Environ() {
		name, _, _ := strings.Cut(pair, "=")
		switch strings.ToUpper(name) {
		case "HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA", "BRAIN_HOME", "PATH":
			continue
		}
		env = append(env, pair)
	}
	cmd.Env = append(env, "HOME="+d.home, "USERPROFILE="+d.home, "XDG_CONFIG_HOME="+d.home, "APPDATA="+d.home, "BRAIN_HOME="+d.state, "PATH=")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	started := time.Now()
	err := cmd.Run()
	row.DurationMS = time.Since(started).Milliseconds()
	row.Stdout = stdout.String()
	row.Stderr = stderr.String()
	if err == nil {
		row.Exit = 0
	} else {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			row.Exit = exit.ExitCode()
		}
	}
	d.commands = append(d.commands, row)
	return row
}
func indexReply(row commandRow, reportedChunks ...*int) ([]struct{ Path, Reason string }, error) {
	if row.Exit != 0 && row.Exit != 1 && row.Exit != 3 {
		return nil, errors.New("index command did not complete")
	}
	var raw struct {
		Skipped    []struct{ Path, Reason string } `json:"skipped"`
		Count      int                             `json:"count"`
		Total      int                             `json:"total"`
		Chunks     int                             `json:"chunks"`
		DurationMS int64                           `json:"duration_ms"`
	}
	if err := json.Unmarshal([]byte(row.Stdout), &raw); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(row.Stdout), &fields); err != nil {
		return nil, err
	}
	if _, hasError := fields["error"]; hasError {
		return nil, errors.New("index returned an error envelope")
	}
	for _, key := range []string{"skipped", "count", "total", "chunks", "duration_ms"} {
		if _, ok := fields[key]; !ok {
			return nil, fmt.Errorf("index reply lacks %s", key)
		}
	}
	if !bytes.HasPrefix(bytes.TrimSpace(fields["skipped"]), []byte("[")) {
		return nil, errors.New("index skipped field is not an array")
	}
	for _, key := range []string{"count", "total", "chunks", "duration_ms"} {
		var number int64
		if bytes.Equal(bytes.TrimSpace(fields[key]), []byte("null")) {
			return nil, fmt.Errorf("index %s is null", key)
		}
		if err := json.Unmarshal(fields[key], &number); err != nil || number < 0 {
			return nil, fmt.Errorf("index %s is not a nonnegative integer", key)
		}
	}
	for _, gap := range raw.Skipped {
		if gap.Path == "" || gap.Reason == "" {
			return nil, errors.New("index gap lacks path or reason")
		}
	}
	if raw.Count != len(raw.Skipped) || raw.Total != raw.Count {
		return nil, errors.New("index skip counts differ")
	}
	if row.Exit == 1 && raw.Count == 0 {
		return nil, errors.New("failed index has no validated gaps")
	}
	if row.Exit != 1 && raw.Count > 0 {
		return nil, errors.New("index gaps need failure exit")
	}
	if len(reportedChunks) > 0 && reportedChunks[0] != nil {
		*reportedChunks[0] = raw.Chunks
	}
	return raw.Skipped, nil
}
func commandSearch(row commandRow) (search.Reply, error) {
	var reply search.Reply
	if row.Exit != 0 {
		return reply, errors.New("search command failed")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(row.Stdout), &raw); err != nil {
		return reply, err
	}
	for _, key := range []string{"count", "total", "empty", "query", "results", "index_age_s"} {
		if _, ok := raw[key]; !ok {
			return reply, fmt.Errorf("search reply lacks %s", key)
		}
	}
	if err := json.Unmarshal([]byte(row.Stdout), &reply); err != nil {
		return reply, err
	}
	if reply.Count != len(reply.Results) || reply.Total < reply.Count || reply.Empty != (reply.Count == 0) || reply.Results == nil || reply.Query.AsRun == "" || reply.Query.Mode == "" {
		return reply, errors.New("invalid search reply fields")
	}
	for _, item := range reply.Results {
		if item.ID == "" || item.Path == "" || item.AlsoIn == nil {
			return reply, errors.New("search result lacks citation identity")
		}
	}
	return reply, nil
}
func completeRank(state, typed string) (search.Reply, int, error) {
	stored, err := index.OpenStored(state)
	if err != nil {
		return search.Reply{}, 0, err
	}
	defer stored.Close()
	_, chunks, _, err := index.Totals(stored.DB)
	if err != nil {
		return search.Reply{}, 0, err
	}
	if chunks > 32765 {
		return search.Reply{}, chunks, errors.New("adapter-capacity-exceeded")
	}
	projects, err := search.Projects(stored.DB)
	if err != nil {
		return search.Reply{}, chunks, err
	}
	plan := query.Make(typed, projects)
	if plan == nil {
		return search.Reply{}, chunks, errors.New("query has no terms")
	}
	reply, err := search.Find(stored.DB, plan, "", "", chunks, false)
	return reply, chunks, err
}
func parity(public, full search.Reply) error {
	want := len(full.Results)
	if want > 100 {
		want = 100
	}
	if public.Total != full.Total || public.Query != full.Query || public.Count != len(public.Results) || public.Count != want {
		return errors.New("public search differs from production adapter")
	}
	for i, item := range public.Results {
		other := full.Results[i]
		if item.ID != other.ID || item.Path != other.Path || !reflect.DeepEqual(item.AlsoIn, other.AlsoIn) {
			return fmt.Errorf("public prefix mismatch at rank %d", i+1)
		}
	}
	return nil
}
