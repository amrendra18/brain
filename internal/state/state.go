package state

import (
	"brain/internal/chunk"
	"brain/internal/corpus"
	"brain/internal/index"
	"database/sql"
	"path/filepath"
	"strings"
	"time"
)

type Source struct {
	Path  string `json:"path"`
	Files int    `json:"files"`
}
type Summary struct {
	Present       bool       `json:"present"`
	Files         int        `json:"files"`
	Chunks        int        `json:"chunks"`
	Duplicates    int        `json:"duplicates_folded"`
	DBBytes       int64      `json:"db_bytes"`
	IndexAgeS     *int       `json:"index_age_s"`
	Pending       int        `json:"pending"`
	PendingKnown  bool       `json:"pending_known"`
	IndexComplete bool       `json:"index_complete"`
	Gaps          index.Gaps `json:"gaps"`
}
type View struct {
	StateDir string   `json:"state_dir"`
	Index    Summary  `json:"index"`
	Sources  []Source `json:"sources"`
}
type Project struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Purpose     string `json:"purpose"`
	Files       int    `json:"files"`
	LastIndexed string `json:"last_indexed"`
}
type Document struct {
	Path     string `json:"path"`
	Project  string `json:"project"`
	Kind     string `json:"kind"`
	Modified string `json:"modified"`
}

func Snapshot(folder string, config corpus.Config, db *sql.DB) (View, []string, error) {
	missing := []string{}
	view := View{StateDir: folder, Index: Summary{DBBytes: index.Bytes(folder), Gaps: index.MergeGaps()}, Sources: []Source{}}
	if db == nil {
		return view, missing, nil
	}
	tx, err := db.Begin()
	if err != nil {
		return view, missing, err
	}
	defer tx.Rollback()
	last, complete, err := index.ReadTime(tx)
	if err != nil {
		return view, missing, err
	}
	view.Index.IndexComplete = complete
	if last != 0 {
		age := max(0, int(float64(time.Now().UnixNano())/1e9-last))
		view.Index.Present = true
		view.Index.IndexAgeS = &age
	}
	view.Index.Files, view.Index.Chunks, view.Index.Duplicates, err = index.Totals(tx)
	if err != nil {
		return view, missing, err
	}
	view.Index.Gaps, err = index.StoredGaps(tx)
	if err != nil {
		return view, missing, err
	}
	rows, err := tx.Query("SELECT source,count(*) FROM files WHERE deleted_at IS NULL GROUP BY source ORDER BY source")
	if err != nil {
		return view, missing, err
	}
	for rows.Next() {
		var source Source
		if err = rows.Scan(&source.Path, &source.Files); err != nil {
			rows.Close()
			return view, missing, err
		}
		view.Sources = append(view.Sources, source)
	}
	err = rows.Err()
	rows.Close()
	if err == nil {
		err = tx.Commit()
	}
	return view, missing, err
}
func iso(seconds float64) string {
	return time.Unix(int64(seconds), 0).UTC().Format("2006-01-02T15:04:05Z")
}

// Purpose returns the first prose paragraph, capped at 200 characters.
func Purpose(text string) string {
	if strings.HasPrefix(text, "---\n") {
		end := strings.Index(text[3:], "\n---")
		if end < 0 {
			return ""
		}
		text = text[end+7:]
	}
	lines := []string{}
	var fence chunk.Fence
	for _, line := range strings.Split(text, "\n") {
		if fence.Consume(line) {
			if len(lines) > 0 {
				break
			}
			continue
		}
		line = strings.TrimSpace(line)
		if len(lines) == 0 {
			skip := false
			for _, marker := range []string{"#", "---", "***", "<", "![", "[![", "|"} {
				if strings.HasPrefix(line, marker) {
					skip = true
				}
			}
			if skip {
				continue
			}
		}
		if line != "" {
			lines = append(lines, line)
		} else if len(lines) > 0 {
			break
		}
	}
	runes := []rune(strings.Join(strings.Fields(strings.Join(lines, " ")), " "))
	return strings.TrimSpace(string(runes[:min(200, len(runes))]))
}

func Projects(db *sql.DB, config corpus.Config) ([]Project, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	projects, err := ProjectsIn(tx, config)
	if err == nil {
		err = tx.Commit()
	}
	return projects, err
}

// Brief reads one stored snapshot without scanning source files.
func Brief(db *sql.DB, config corpus.Config, folder string) (files, chunks int, last float64, project Project, err error) {
	tx, err := db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	files, chunks, _, err = index.Totals(tx)
	if err != nil {
		return
	}
	last, err = index.ScannedAt(tx)
	if err != nil {
		return
	}
	items, err := ProjectsIn(tx, config)
	if err != nil {
		return
	}
	for _, item := range items {
		if relative, problem := filepath.Rel(item.Path, folder); problem == nil && (relative == "." || filepath.IsLocal(relative)) {
			project = item
		}
	}
	err = tx.Commit()
	return
}
func ProjectsIn(db index.Reader, config corpus.Config) ([]Project, error) {
	rows, err := db.Query("SELECT project, coalesce(min(CASE WHEN kind!='note' THEN source||'/'||path END),min(source||'/'||path)), sum(status IN ('current','superseded')), max(indexed_at) FROM files WHERE deleted_at IS NULL GROUP BY project ORDER BY project")
	if err != nil {
		return nil, err
	}
	projects := []Project{}
	for rows.Next() {
		var project Project
		var first string
		var newest float64
		if err = rows.Scan(&project.Name, &first, &project.Files, &newest); err != nil {
			rows.Close()
			return nil, err
		}
		project.Path = filepath.Dir(filepath.FromSlash(first))
		project.LastIndexed = iso(newest)
		for i := len(config.Sources) - 1; i >= 0; i-- {
			source := config.Sources[i]
			roots := []string{source.Path}
			if corpus.SourcePattern(source) {
				roots = nil
				for root := filepath.Dir(filepath.FromSlash(first)); ; root = filepath.Dir(root) {
					if corpus.MatchesRoot(source, root) {
						roots = append(roots, root)
						break
					}
					if filepath.Dir(root) == root {
						break
					}
				}
			}
			owned := false
			for _, root := range roots {
				relative, problem := filepath.Rel(root, filepath.FromSlash(first))
				if problem != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
					continue
				}
				project.Path = root
				if source.Project == "" && source.Kind != "note" {
					parts := strings.Split(relative, string(filepath.Separator))
					if len(parts) > 1 {
						project.Path = filepath.Join(root, parts[0])
					}
				}
				owned = true
				break
			}
			if owned {
				break
			}
		}
		projects = append(projects, project)
	}
	problem := rows.Err()
	rows.Close()
	if problem != nil {
		return nil, problem
	}
	for i := range projects {
		project := &projects[i]
		rows, err = db.Query("SELECT id,source,path FROM files WHERE project=? AND kind='readme' AND deleted_at IS NULL AND status IN ('current','superseded') ORDER BY length(source||'/'||path),source,path", project.Name)
		if err != nil {
			return nil, err
		}
		var chosen int64
		for rows.Next() {
			var id int64
			var root, relative string
			if err = rows.Scan(&id, &root, &relative); err != nil {
				rows.Close()
				return nil, err
			}
			if filepath.Dir(filepath.Join(root, filepath.FromSlash(relative))) == project.Path {
				chosen = id
				break
			}
		}
		problem = rows.Err()
		rows.Close()
		if problem != nil {
			return nil, problem
		}
		if chosen == 0 {
			continue
		}
		text, err := index.DocumentText(db, chosen)
		if err != nil {
			return nil, err
		}
		project.Purpose = Purpose(text)
	}
	return projects, nil
}
func Recent(db *sql.DB, days, limit int) ([]Document, int, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	documents, total, err := recent(tx, days, limit)
	if err == nil {
		err = tx.Commit()
	}
	return documents, total, err
}
func recent(db index.Reader, days, limit int) ([]Document, int, error) {
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour).UnixNano()
	where := " FROM files WHERE deleted_at IS NULL AND status IN ('current','superseded') AND mtime_ns>=?"
	var total int
	if err := db.QueryRow("SELECT count(*)"+where, since).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := db.Query("SELECT source,path,project,kind,mtime_ns"+where+" ORDER BY mtime_ns DESC,source,path LIMIT ?", since, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	documents := []Document{}
	for rows.Next() {
		var document Document
		var root, relative string
		var mtime int64
		if err = rows.Scan(&root, &relative, &document.Project, &document.Kind, &mtime); err != nil {
			return nil, 0, err
		}
		document.Path = filepath.Join(root, filepath.FromSlash(relative))
		document.Modified = iso(float64(mtime) / 1e9)
		documents = append(documents, document)
	}
	problem := rows.Err()
	rows.Close()
	return documents, total, problem
}
