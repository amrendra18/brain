package verifyrun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"time"
)

var errContractOutputLimit = errors.New("contract command exceeded 1 MiB of output")

type contractOutput struct {
	mu       sync.Mutex
	stdout   bytes.Buffer
	stderr   bytes.Buffer
	count    int
	overflow bool
	command  *exec.Cmd
}

type contractStream struct {
	output *contractOutput
	stderr bool
}

func (stream contractStream) Write(data []byte) (int, error) {
	output := stream.output
	output.mu.Lock()
	remaining := 1<<20 - output.count
	if remaining < 0 {
		remaining = 0
	}
	kept := len(data)
	if kept > remaining {
		kept = remaining
	}
	if stream.stderr {
		_, _ = output.stderr.Write(data[:kept])
	} else {
		_, _ = output.stdout.Write(data[:kept])
	}
	output.count += kept
	overflow := kept != len(data)
	output.overflow = output.overflow || overflow
	output.mu.Unlock()
	if overflow {
		_ = terminateContractProcessGroup(output.command)
		return kept, errContractOutputLimit
	}
	return len(data), nil
}

func (d *driver) contractCommand(home, source string, expected int, args ...string) ([]byte, error) {
	base := filepath.Dir(home)
	before, err := snapshot(base)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, d.binary, args...)
	command.Dir = source
	command.Env = []string{
		"HOME=" + home, "USERPROFILE=" + home,
		"APPDATA=" + filepath.Join(home, "config"), "LOCALAPPDATA=" + filepath.Join(home, "data"),
		"XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "XDG_DATA_HOME=" + filepath.Join(home, "data"),
		"TMPDIR=" + home, "TEMP=" + home, "TMP=" + home,
		"BRAIN_HOME=" + filepath.Join(home, "state"),
	}
	for _, name := range []string{"PATH", "SystemRoot", "WINDIR"} {
		if value := os.Getenv(name); value != "" {
			command.Env = append(command.Env, name+"="+value)
		}
	}
	setContractProcessGroup(command)
	command.Cancel = func() error { return terminateContractProcessGroup(command) }
	command.WaitDelay = 2 * time.Second
	output := &contractOutput{command: command}
	command.Stdout = contractStream{output: output}
	command.Stderr = contractStream{output: output, stderr: true}
	start := time.Now()
	startErr := command.Start()
	var waitErr error
	if startErr == nil {
		waitErr = command.Wait()
	}
	leftover := startErr == nil && contractProcessGroupAlive(command)
	_ = terminateContractProcessGroup(command)
	var code *int
	if command.ProcessState != nil {
		value := command.ProcessState.ExitCode()
		code = &value
	}
	record := Command{
		Argv: append([]string{d.binary}, args...), Exit: code, Started: startErr == nil,
		Stdin: "closed", DurationMS: time.Since(start).Milliseconds(),
		Stdout: output.stdout.String(), Stderr: output.stderr.String(),
	}
	d.summary.Commands = append(d.summary.Commands, record)
	number := len(d.summary.Commands)
	if err := writeJSON(filepath.Join(d.evidence, "logs", fmt.Sprintf("%02d-command.json", number)), record); err != nil {
		return nil, err
	}
	after, err := snapshot(base)
	if err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(d.evidence, "traces", fmt.Sprintf("contract-command-%02d-files.json", number)), map[string]any{"before": before, "after": after}); err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(before, after) {
		return nil, errors.New("contract command changed isolated home, state, or sources")
	}
	if startErr != nil {
		return nil, fmt.Errorf("%w: contract target did not start: %v", errTargetUnready, startErr)
	}
	if output.overflow {
		return nil, errContractOutputLimit
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("contract target reached its deadline: %w", ctx.Err())
	}
	if leftover {
		return nil, errors.New("contract target left a child process")
	}
	var exitError *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitError) {
		return nil, fmt.Errorf("contract target could not finish: %w", waitErr)
	}
	if code == nil || *code != expected || record.Stdin != "closed" || len(record.Stdout) == 0 || len(record.Stdout) > 128*1024 || record.Stderr != "" {
		return nil, fmt.Errorf("contract command exit, output, or closed input is invalid: expected exit %d", expected)
	}
	return []byte(record.Stdout), nil
}
