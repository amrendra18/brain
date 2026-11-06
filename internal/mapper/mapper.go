package mapper

import (
	"brain/internal/chunk"
	"brain/internal/corpus"
	"brain/internal/index"
	"brain/internal/notes"
	"brain/internal/platform"
	"brain/internal/redact"
	"brain/internal/state"
	"database/sql"
	"path/filepath"
	"strings"
	"unicode"
)

type Milestones struct {
	Count int      `json:"count"`
	Total int      `json:"total"`
	Empty bool     `json:"empty"`
	Lines []string `json:"lines"`
}
type Entry struct {
	When string `json:"when"`
	Text string `json:"text"`
}
type Notes struct {
	Count   int     `json:"count"`
	Total   int     `json:"total"`
	Empty   bool    `json:"empty"`
	Entries []Entry `json:"entries"`
}
type Page struct {
	Project    string         `json:"project"`
	Path       string         `json:"path"`
	Purpose    string         `json:"purpose"`
	Commit     *Commit        `json:"commit"`
	Milestones Milestones     `json:"milestones"`
	Unchecked  map[string]int `json:"unchecked"`
	Notes      Notes          `json:"notes"`
	ScannedAt  float64        `json:"-"`
}

func cut(text string, limit int) string {
	runes := []rune(text)
	return strings.TrimRightFunc(string(runes[:min(limit, len(runes))]), unicode.IsSpace)
}

func documentLines(text string) []string {
	text = strings.NewReplacer("\r\n", "\n", "\r", "\n", "\v", "\n", "\f", "\n", "\u001c", "\n", "\u001d", "\n", "\u001e", "\n", "\u0085", "\n", "\u2028", "\n", "\u2029", "\n").Replace(text)
	return strings.Split(text, "\n")
}

func StatusLines(text string) []string {
	found := []string{}
	inside := false
	var fence chunk.Fence
	for _, line := range documentLines(text) {
		stripped := strings.TrimSpace(line)
		heading := strings.TrimLeft(line, " ")
		spaces := len(line) - len(heading)
		hashes := len(heading) - len(strings.TrimLeft(heading, "#"))
		if !fence.Consume(line) && spaces <= 3 && hashes >= 1 && hashes <= 6 && len(heading) > hashes && (heading[hashes] == ' ' || heading[hashes] == '\t') {
			if inside {
				break
			}
			inside = strings.Contains(strings.ToLower(stripped), "status")
			continue
		}
		if inside && stripped != "" {
			runes := []rune(stripped)
			found = append(found, string(runes[:min(120, len(runes))]))
		}
	}
	return found
}

func Unchecked(text string) int {
	count := 0
	var fence chunk.Fence
	for _, line := range documentLines(text) {
		if !fence.Consume(line) && strings.HasPrefix(strings.TrimLeftFunc(line, unicode.IsSpace), "- [ ]") {
			count++
		}
	}
	return count
}

func Entries(stateFolder, project string, policies ...*redact.Policy) []Entry {
	policy := redact.Default()
	if len(policies) > 0 {
		policy = policies[0]
	}
	found := []Entry{}
	if !notes.Project(project) {
		return found
	}
	data, err := platform.ReadState(stateFolder, filepath.Join("notes", project+".md"))
	if err != nil {
		return found
	}
	var current *Entry
	for _, line := range strings.Split(policy.Apply(strings.ToValidUTF8(string(data), "\ufffd")).Text, "\n") {
		if strings.HasPrefix(line, "## ") {
			if current != nil {
				found = append(found, *current)
			}
			current = &Entry{When: strings.Join(strings.Fields(line[3:]), " ")}
		} else if current != nil {
			current.Text += " " + line
		}
	}
	if current != nil {
		found = append(found, *current)
	}
	for i := range found {
		found[i].Text = cut(strings.Join(strings.Fields(found[i].Text), " "), 200)
	}
	for i, j := 0, len(found)-1; i < j; i, j = i+1, j-1 {
		found[i], found[j] = found[j], found[i]
	}
	return found
}

// Build reads every indexed fact from one SQLite snapshot.
func Build(db *sql.DB, config corpus.Config, folder, name string) (Page, []string, error) {
	page := Page{Milestones: Milestones{Empty: true, Lines: []string{}}, Unchecked: map[string]int{}, Notes: Notes{Empty: true, Entries: []Entry{}}}
	tx, err := db.Begin()
	if err != nil {
		return page, nil, err
	}
	defer tx.Rollback()
	projects, err := state.ProjectsIn(tx, config)
	if err != nil {
		return page, nil, err
	}
	names := []string{}
	for _, project := range projects {
		names = append(names, project.Name)
	}
	resolved, err := state.ResolveProject(names, name)
	if err != nil {
		return page, names, err
	}
	for _, project := range projects {
		if project.Name == resolved {
			page.Project, page.Path, page.Purpose = project.Name, project.Path, project.Purpose
		}
	}
	if page.Project == "" {
		return page, names, nil
	}
	for _, file := range []string{"PLAN.md", "GOAL.md"} {
		text, err := indexedText(tx, page.Project, filepath.Join(page.Path, file))
		if err != nil {
			return page, names, err
		}
		page.Unchecked[file] = Unchecked(text)
		if file == "PLAN.md" {
			lines := StatusLines(text)
			page.Milestones = Milestones{min(20, len(lines)), len(lines), len(lines) == 0, lines[:min(20, len(lines))]}
		}
	}
	page.ScannedAt, err = index.ScannedAt(tx)
	if err != nil {
		return page, names, err
	}
	if err = tx.Commit(); err != nil {
		return page, names, err
	}
	entries := Entries(folder, page.Project, config.Privacy())
	page.Notes = Notes{min(3, len(entries)), len(entries), len(entries) == 0, entries[:min(3, len(entries))]}
	page.Commit = LatestCommit(page.Path, config.Privacy())
	return page, names, nil
}

func indexedText(db index.Reader, project, path string) (string, error) {
	rows, err := db.Query("SELECT id,source,path FROM files WHERE project=? AND deleted_at IS NULL AND status IN ('current','superseded') ORDER BY source,path", project)
	if err != nil {
		return "", err
	}
	var chosen int64
	for rows.Next() {
		var id int64
		var source, relative string
		if err = rows.Scan(&id, &source, &relative); err != nil {
			rows.Close()
			return "", err
		}
		if filepath.Join(source, filepath.FromSlash(relative)) == path {
			chosen = id
			break
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil || chosen == 0 {
		return "", err
	}
	return index.DocumentText(db, chosen)
}
