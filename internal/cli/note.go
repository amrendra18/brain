package cli

import (
	"brain/internal/corpus"
	"brain/internal/index"
	"brain/internal/notes"
	"brain/internal/platform"
	"brain/internal/search"
	"brain/internal/state"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func here(config corpus.Config) string {
	path, err := os.Getwd()
	if err != nil {
		return "general"
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "general"
	}
	project := "general"
	for _, source := range config.Sources {
		if source.Kind == "note" {
			continue
		}
		roots := []string{source.Path}
		if corpus.SourcePattern(source) {
			prefix := filepath.Dir(source.Path[:strings.IndexAny(source.Path, "*?")])
			if resolved, err := filepath.EvalSymlinks(prefix); err == nil {
				relative, _ := filepath.Rel(prefix, source.Path)
				source.Path = filepath.Join(resolved, relative)
			}
			roots = nil
			for root := path; ; root = filepath.Dir(root) {
				if corpus.MatchesRoot(source, root) {
					roots = append(roots, root)
					break
				}
				if filepath.Dir(root) == root {
					break
				}
			}
		}
		for _, root := range roots {
			root, err = filepath.EvalSymlinks(root)
			if err != nil {
				continue
			}
			relative, err := filepath.Rel(root, path)
			if err != nil || relative != "." && !filepath.IsLocal(relative) {
				continue
			}
			project = filepath.Base(root)
			if relative != "." {
				project = strings.Split(relative, string(filepath.Separator))[0]
			}
			if source.Project != "" {
				project = source.Project
			}
		}
	}
	return project
}

func (call *call) note(config corpus.Config) int {
	raw := strings.Join(call.words, " ")
	if len(raw) > notes.LimitBytes {
		return call.failure("note-too-long", fmt.Sprintf("The note exceeds the %d-byte input limit. No note was saved. Run help note for the supported size.", notes.LimitBytes), 2, []string{"help", "note"})
	}
	text := config.Privacy().Apply(strings.TrimSpace(raw)).Text
	project := call.flags["--project"]
	if project == "" {
		project = here(config)
	}
	if config.Privacy().Match(project) {
		return call.privacyFailure(index.ErrPrivacyPath)
	}
	if !notes.Project(project) {
		return call.usage("invalid-value", "Invalid --project value "+inputName(project)+". Use up to 100 letters, numbers, underscores, spaces, dots or hyphens. Avoid reserved file names and trailing spaces or dots.")
	}
	if !platform.Writable(call.state) {
		return call.unwritable()
	}
	if err := platform.CheckPrivateFile(call.state, filepath.Join("locks", "index.lock")); err != nil {
		return call.databaseError(err)
	}
	db, openErr := index.OpenRead(call.state, false)
	if openErr != nil && !index.Rebuildable(openErr) && !index.Locked(openErr) && !errors.Is(openErr, platform.ErrLocked) {
		return call.databaseError(openErr)
	}
	if openErr == nil {
		known, err := search.Projects(db.DB)
		if err == nil {
			resolved, err := state.ResolveProject(known, project)
			if err != nil {
				db.Close()
				return call.ambiguousProject()
			}
			if resolved != "" {
				project = resolved
			}
		}
		db.Close()
	}
	block := notes.Entry(text, time.Now().UTC().Format(time.RFC3339))
	path, err := notes.Append(call.state, project, block, config.Privacy())
	if err != nil {
		return call.failure("notes-unwritable", "Brain could not confirm the note save. Check the notes before retrying. doctor checks the folder.", 1, []string{"doctor"})
	}
	identifier := notes.EntryID(project, block)
	found := 0
	writable, err := index.Open(call.state, true, false, false)
	if err == nil {
		defer writable.Close()
		if err = index.Note(writable, call.state, config, project); err == nil {
			_ = writable.QueryRow("SELECT count(*) FROM places JOIN files ON files.id=places.file WHERE files.namespace='notes' AND files.path=? AND places.chunk=? AND files.deleted_at IS NULL", project+".md", identifier).Scan(&found)
		}
	}
	if call.flags["--format"] == "json" {
		return WriteJSON(call.output, struct {
			Path    string `json:"path"`
			EntryID string `json:"entry_id"`
			Indexed bool   `json:"indexed"`
		}{path, identifier, found > 0})
	}
	indexed := "Not indexed yet: run index to add the saved note."
	if found > 0 {
		indexed = "Indexed: search finds it now."
	}
	if _, err = writeText(call.output, fmt.Sprintf("Saved to %s, entry %s.\n%s\n", path, identifier, indexed)); err != nil {
		return 1
	}
	return 0
}
