package verifyrun

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type Feature struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type Summary struct {
	Level          string            `json:"level"`
	Exit           int               `json:"exit"`
	BinarySHA256   string            `json:"binary_sha256"`
	SourceTree     string            `json:"source_tree"`
	Features       []Feature         `json:"features"`
	Commands       []Command         `json:"commands"`
	StandIns       []string          `json:"stand_ins"`
	Scratch        string            `json:"scratch"`
	ScratchRemoved bool              `json:"scratch_removed"`
	SideEffects    map[string]string `json:"side_effects"`
}

var errTargetUnready = errors.New("target could not launch")

const usage = `Usage: scripts/verify [fast|full] [--feature cited-search|tool-contract|skill-drift|config-sources|freshness|privacy|exclusions|schedule|settings-relocation] [--binary FILE] [--expected-owner NAME]
fast drives each mapped feature once. full repeats it against fresh state.
The default target is built from the current source tree before each check.
--binary checks an outside executable; its source identity is unverified.
--expected-owner deliberately changes the expected project for a failure control.
Exits: 0 every selected feature proved; 1 feature failed; 2 usage error; 3 environment unready before target launch.
Feature map: verify/features/. Evidence: .local/verify/.
`

func Run(root string, args []string, output io.Writer) int {
	level := "fast"
	if len(args) > 0 && (args[0] == "fast" || args[0] == "full") {
		level, args = args[0], args[1:]
	}
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.Usage = func() { fmt.Fprint(output, usage) }
	feature := flags.String("feature", "", "feature to check; empty selects every mapped feature")
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	binary := flags.String("binary", "", "outside executable; source identity unverified")
	owner := flags.String("expected-owner", "harbor", "expected project")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || (*feature != "" && !knownFeature(*feature)) || *owner == "" {
		fmt.Fprint(output, usage)
		return 2
	}
	evidenceBase := filepath.Join(root, ".local", "verify")
	if err := os.MkdirAll(evidenceBase, 0700); err != nil {
		fmt.Fprintln(output, "Environment unready:", err)
		return 3
	}
	evidence, err := os.MkdirTemp(evidenceBase, time.Now().UTC().Format("20060102T150405Z")+"-")
	if err != nil {
		fmt.Fprintln(output, "Environment unready:", err)
		return 3
	}
	summary := Summary{Level: level, Features: []Feature{}, Commands: []Command{}, StandIns: []string{}, SideEffects: map[string]string{}}
	selected := make([]string, 0, len(featureJourneys))
	for _, item := range featureJourneys {
		selected = append(selected, item.name)
	}
	if *feature != "" {
		selected = []string{*feature}
	}
	run := driver{repo: root, evidence: evidence, summary: &summary}
	target := *binary
	var problem error
	if target == "" {
		target = filepath.Join(root, ".local", "bin", "brain"+suffix)
		problem = run.build(target)
	} else {
		summary.SourceTree = "unverified (outside executable)"
	}
	if problem == nil {
		problem = run.prepare(target)
	}
	if problem != nil {
		summary.Exit = 3
		for _, name := range selected {
			summary.Features = append(summary.Features, Feature{name, "skipped", "Environment unready: " + problem.Error()})
		}
	} else {
		attempts := 1
		if level == "full" {
			attempts = 2
		}
		for _, name := range selected {
			problem = nil
			for i := 0; i < attempts; i++ {
				for _, item := range featureJourneys {
					if item.name == name {
						problem = item.run(&run, *owner, i)
						break
					}
				}
				if problem != nil {
					break
				}
			}
			row := Feature{name, "proven", fmt.Sprintf("%d isolated journey(s) proved %s; command and side-effect evidence retained", attempts, name)}
			if problem != nil {
				if summary.Exit == 0 {
					summary.Exit = 1
				}
				row.Status, row.Reason = "failed", problem.Error()
				if errors.Is(problem, errTargetUnready) {
					anyStarted := false
					for _, command := range summary.Commands {
						anyStarted = anyStarted || command.Started
					}
					if !anyStarted {
						summary.Exit = 3
						row.Status = "skipped"
						row.Reason = "Environment unready: " + problem.Error()
					}
				}
			}
			summary.Features = append(summary.Features, row)
		}
	}
	if summary.Scratch != "" {
		if err := os.RemoveAll(summary.Scratch); err != nil {
			summary.Exit = 1
			for i := range summary.Features {
				summary.Features[i].Status = "failed"
				summary.Features[i].Reason += "; scratch cleanup: " + err.Error()
			}
		} else {
			summary.ScratchRemoved = true
		}
	}
	if err := run.save(); err != nil {
		summary.Exit = 1
		fmt.Fprintln(output, "Evidence write failed:", err)
	}
	for _, row := range summary.Features {
		fmt.Fprintf(output, "%s: %s (%s)\n", row.Name, row.Status, row.Reason)
	}
	fmt.Fprintln(output, evidence)
	return summary.Exit
}
