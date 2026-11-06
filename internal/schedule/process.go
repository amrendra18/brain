package schedule

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"time"
)

const processLimit = 64 * 1024
const processTimeout = 15 * time.Second

type ProcessResult struct {
	Output   string
	ExitCode int
}

type Runner func(context.Context, Invocation) (ProcessResult, error)

var ErrUnavailable = errors.New("the user scheduler is unavailable or did not answer safely")
var ErrActivation = errors.New("the daily schedule could not be activated; retry schedule --write after checking the user scheduler")
var ErrCollision = errors.New("the schedule destination or job is not owned by this state; preserve it and choose a different state folder")
var ErrFiles = errors.New("the schedule files cannot be read or written safely")
var ErrBusy = errors.New("another process is changing this state's daily schedule")

type boundedOutput struct{ strings.Builder }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > processLimit {
		return 0, ErrUnavailable
	}
	return b.Builder.Write(p)
}

func nativeRunner(ctx context.Context, call Invocation) (ProcessResult, error) {
	command := exec.CommandContext(ctx, call.Executable, call.Args...)
	command.WaitDelay = time.Second
	var output boundedOutput
	command.Stdout = &output
	command.Stderr = io.Discard
	err := command.Run()
	result := ProcessResult{Output: output.String()}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		result.ExitCode = exit.ExitCode()
		err = nil
	}
	return result, err
}

func run(runner Runner, call Invocation) (ProcessResult, error) {
	if runner == nil {
		runner = nativeRunner
	}
	ctx, cancel := context.WithTimeout(context.Background(), processTimeout)
	defer cancel()
	result, err := runner(ctx, call)
	if err != nil || ctx.Err() != nil || len(result.Output) > processLimit {
		return ProcessResult{}, ErrUnavailable
	}
	return result, nil
}
