package mapper

import (
	"brain/internal/platform"
	"brain/internal/redact"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Commit struct {
	Date    string `json:"date"`
	Subject string `json:"subject"`
}

type boundedOutput []byte

func (output *boundedOutput) Write(data []byte) (int, error) {
	*output = append(*output, data[:min(len(data), 4096-len(*output))]...)
	return len(data), nil
}

// LatestCommit runs only Git's log reader, with fetching, hooks and pagers disabled.
func LatestCommit(folder string, policies ...*redact.Policy) *Commit {
	policy := redact.Default()
	if len(policies) > 0 {
		policy = policies[0]
	}
	if _, err := os.Stat(filepath.Join(folder, ".git")); err != nil {
		return nil
	}
	git := platform.GitPath()
	if git == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	args := []string{"--no-pager", "--no-optional-locks", "-c", "core.fsmonitor=false", "-c", "maintenance.auto=false", "-c", "gc.auto=0", "-c", "log.showSignature=false", "-C", folder, "log", "-1", "--no-show-signature", "--no-decorate", "--format=%cI%n%s"}
	command := exec.CommandContext(ctx, git, args...)
	command.WaitDelay = 100 * time.Millisecond
	environment := []string{}
	for _, value := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(value), "GIT_") {
			environment = append(environment, value)
		}
	}
	command.Env = append(environment, "GIT_OPTIONAL_LOCKS=0", "GIT_NO_LAZY_FETCH=1", "GIT_NO_REPLACE_OBJECTS=1", "GIT_ALLOW_PROTOCOL=", "GIT_PROTOCOL_FROM_USER=0", "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_SYSTEM="+os.DevNull, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_PAGER=", "PAGER=")
	var output boundedOutput
	command.Stdout, command.Stderr = &output, io.Discard
	if err := command.Run(); err != nil {
		return nil
	}
	if len(output) >= 4096 {
		return nil
	}
	date, subject, _ := strings.Cut(string(output), "\n")
	subject = policy.Apply(subject).Text
	stamp, err := time.Parse(time.RFC3339, strings.TrimSpace(date))
	if err != nil {
		return nil
	}
	return &Commit{stamp.UTC().Format("2006-01-02T15:04:05Z"), cut(strings.Join(strings.Fields(subject), " "), 200)}
}
