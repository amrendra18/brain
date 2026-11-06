package skill

import (
	"fmt"
	"strings"
)

type Command struct{ Name, Purpose, Example string }

// Text uses the help's purposes and examples without embedding a machine's path.
func Text(commands []Command) string {
	lines := []string{"---", "name: brain", "description: Search selected project files before answering questions about those projects or past decisions.", "---", "", "# Brain", "",
		"Search local Markdown, DOCX and RTF files before answering about past work. Cite the returned file path.", "",
		"Put brain (brain.exe on Windows) on your command path. These commands work in every shell.", "Add --format json for one compact JSON reply. No model or network is used. Run index only for an explicit update or an unusable index after other routes failed."}
	for _, command := range commands {
		lines = append(lines, "", "## "+command.Name, "", command.Purpose, "```", "brain "+command.Example, "```")
	}
	lines = append(lines, "", "## Reply rules", "", "- Every reading reply carries index_age_s. It reports the index age in seconds. index_complete says whether a complete source scan has finished.", "- Run the needed next command exactly as printed. next_argv runs without a shell. After a refusal, the first command gives recovery guidance.", "", "For other commands and flags, run `brain help COMMAND`.")
	return strings.Join(lines, "\n") + "\n"
}

type Facts struct {
	Problem, Next, Doctor, Name, Purpose   string
	Files, Chunks, Age, ProjectFiles, Gaps int
	IndexComplete                          bool
	ReadOnly                               bool
}

// Brief is three bounded lines, including the age of the stored index.
func Brief(facts Facts) string {
	fileWord := func(count int) string {
		if count == 1 {
			return "file"
		}
		return "files"
	}
	commands := func(label, kind string, doctorFirst bool) []string {
		search, doctor := label+facts.Next, "Doctor for "+kind+" guidance: "+facts.Doctor
		both := search + ". " + doctor
		if doctorFirst {
			both = doctor + ". " + search
		}
		return []string{both, search + ". The doctor command cannot fit this brief. Run the binary's help command for " + kind + " guidance."}
	}
	lines := []string{
		fmt.Sprintf("Brain index: %d %s, %d chunks, last scan %d s ago; stored read-only snapshot. Run index explicitly to refresh sources.", facts.Files, fileWord(facts.Files), facts.Chunks, facts.Age),
		"Project: no project under this folder.",
		"The command cannot fit this brief. Run the binary's help command for recovery guidance.",
	}
	choices := commands("Search: ", "write", false)
	if !facts.IndexComplete {
		lines[0] = "Brain index: stored documents are available; no complete source scan has finished."
	}
	if facts.Name != "" {
		lines[1] = fmt.Sprintf("Project %s: %d %s in the index.", facts.Name, facts.ProjectFiles, fileWord(facts.ProjectFiles))
		if facts.Purpose != "" {
			lines[1] += " README: " + facts.Purpose
		}
	}
	if facts.Problem != "" {
		lines = []string{"Brain index: not ready, because " + facts.Problem, "Project: unknown until the index is ready.", lines[2]}
		choices = []string{"Run first: " + facts.Next}
	} else if facts.ReadOnly {
		choices = commands("Search stored data: ", "write", true)
	}
	if facts.Problem == "" && facts.Gaps > 0 {
		lines[0] = fmt.Sprintf("Index gaps: %d. Some source contents are missing or may be outdated. ", facts.Gaps) + lines[0]
		choices = commands("Search stored data: ", "repair", true)
	}
	for i, line := range lines[:2] {
		line = strings.NewReplacer("\r", `\r`, "\n", `\n`).Replace(line)
		runes := []rune(line)
		lines[i] = string(runes[:min(320, len(runes))])
	}
	for _, choice := range choices {
		if !strings.ContainsAny(choice, "\r\n") && len([]rune(lines[0]))+len([]rune(lines[1]))+len([]rune(choice))+3 < 1000 {
			lines[2] = choice
			break
		}
	}
	return strings.Join(lines, "\n") + "\n"
}
