package cli

import (
	"brain/internal/corpus"
	"brain/internal/mapper"
	"brain/internal/state"
	"errors"
	"fmt"
	"strings"
	"time"
)

type mapReply struct {
	mapper.Page
	metadata
}

func (call *call) mapPage(config corpus.Config) int {
	db, meta, err := call.ready(config)
	if err != nil {
		return call.databaseError(err)
	}
	defer db.Close()
	page, names, err := mapper.Build(db.DB, config, call.state, call.words[0])
	if errors.Is(err, state.ErrProjectAmbiguous) {
		return call.ambiguousProject()
	}
	if err != nil {
		return call.databaseError(err)
	}
	if page.Project == "" {
		return call.failure("project-unknown", "No indexed project is named "+inputName(call.words[0])+". The projects are: "+strings.Trim(inputName(strings.Join(names, ", ")), "\"")+".", 1, []string{"projects"})
	}
	meta.IndexComplete = page.ScannedAt != 0
	if meta.IndexComplete {
		meta.IndexAgeS = max(0, int(float64(time.Now().UnixNano())/1e9-page.ScannedAt))
	}
	reply := mapReply{page, meta}
	if call.flags["--format"] == "json" {
		return WriteJSON(call.output, reply)
	}
	purpose := page.Purpose
	if purpose == "" {
		purpose = "README: no first paragraph found."
	}
	lines := []string{page.Project + ": " + page.Path, purpose, ""}
	commit := "Last commit: none found by Git."
	if page.Commit != nil {
		commit = "Last commit: " + page.Commit.Date + " " + page.Commit.Subject
	}
	lines = append(lines, commit, "", "Milestone status: none in PLAN.md.")
	if page.Milestones.Count > 0 {
		lines[len(lines)-1] = "Milestone status in PLAN.md:"
	}
	for _, line := range page.Milestones.Lines {
		lines = append(lines, "  "+line)
	}
	if page.Milestones.Total > page.Milestones.Count {
		lines = append(lines, fmt.Sprintf("  Lines not shown: %d.", page.Milestones.Total-page.Milestones.Count))
	}
	lines = append(lines, "", fmt.Sprintf("Unchecked boxes: %d in PLAN.md, %d in GOAL.md.", page.Unchecked["PLAN.md"], page.Unchecked["GOAL.md"]), "", "Notes: none.")
	if page.Notes.Count > 0 {
		lines[len(lines)-1] = "Newest notes:"
	}
	for _, entry := range page.Notes.Entries {
		lines = append(lines, "  "+entry.When+"  "+entry.Text)
	}
	if page.Notes.Total > page.Notes.Count {
		lines = append(lines, fmt.Sprintf("  Older notes not shown: %d.", page.Notes.Total-page.Notes.Count))
	}
	lines = append(lines, "", fmt.Sprintf("Index age %d s.", meta.IndexAgeS))
	for i, line := range lines {
		lines[i] = strings.NewReplacer("\r", `\r`, "\n", `\n`, "\v", `\v`, "\f", `\f`, "\u0085", `\u0085`, "\u2028", `\u2028`, "\u2029", `\u2029`).Replace(line)
	}
	if !meta.Gaps.Empty {
		lines = append(lines, fmt.Sprintf("Index gaps: %d. Some source contents are missing or may be outdated. Run doctor for repair guidance.", meta.Gaps.Count))
	}
	total := len(lines)
	for _, command := range meta.Next {
		needed := 1 + strings.Count(command, "\n")
		if total+needed >= 60 {
			lines = append(lines, "Next: request this map with --format json for exact next and next_argv.")
			break
		}
		lines = append(lines, "Next: "+textCommand(command))
		total += needed
	}
	if _, err = writeText(call.output, fmt.Sprintln(strings.Join(lines, "\n"))); err != nil {
		return 1
	}
	return 0
}
