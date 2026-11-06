package schedule

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScheduleProcessHelper(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "--schedule-helper" {
		return
	}
	switch os.Args[len(os.Args)-1] {
	case "exit":
		fmt.Print("adapter output")
		os.Exit(3)
	case "large":
		fmt.Print(strings.Repeat("x", processLimit+1))
		os.Exit(0)
	case "wait":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
}

func TestScheduleProcessBoundary(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	call := func(mode string) Invocation {
		return invocation(executable, "-test.run=^TestScheduleProcessHelper$", "--", "--schedule-helper", mode)
	}
	result, err := run(nil, call("exit"))
	if err != nil || result.ExitCode != 3 || result.Output != "adapter output" {
		t.Fatalf("fake process: %+v %v", result, err)
	}
	if _, err := run(nil, call("large")); !errors.Is(err, ErrUnavailable) {
		t.Fatal("unbounded output accepted", err)
	}
	if _, err := run(nil, invocation(filepath.Join(t.TempDir(), "absent"))); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing executable accepted", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _ = nativeRunner(ctx, call("wait"))
	if ctx.Err() == nil || time.Since(start) > time.Second {
		t.Fatal("process timeout did not stop helper")
	}
	if _, err := run(func(context.Context, Invocation) (ProcessResult, error) {
		return ProcessResult{Output: strings.Repeat("x", processLimit+1)}, nil
	}, Invocation{}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("injected output limit missing")
	}
}
