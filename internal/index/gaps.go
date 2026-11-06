package index

import (
	"brain/internal/corpus"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
)

type Gaps struct {
	Items []corpus.Skipped `json:"items"`
	Count int              `json:"count"`
	Total int              `json:"total"`
	Empty bool             `json:"empty"`
}

func MergeGaps(groups ...[]corpus.Skipped) Gaps {
	unique := map[corpus.Skipped]bool{}
	items := []corpus.Skipped{}
	for _, group := range groups {
		for _, item := range group {
			if !unique[item] {
				unique[item] = true
				items = append(items, item)
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Path == items[j].Path {
			return items[i].Reason < items[j].Reason
		}
		return items[i].Path < items[j].Path
	})
	return Gaps{items, len(items), len(items), len(items) == 0}
}

// StoredGaps reports the last completed scan and every unresolved file failure.
func StoredGaps(db Reader) (Gaps, error) {
	items := []corpus.Skipped{}
	var value string
	err := db.QueryRow("SELECT value FROM meta WHERE key='scan_gaps'").Scan(&value)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Gaps{}, err
	}
	if err == nil {
		if err = json.Unmarshal([]byte(value), &items); err != nil {
			return Gaps{}, err
		}
	}
	rows, err := db.Query("SELECT source,path,status FROM files WHERE deleted_at IS NULL AND status NOT IN ('current','superseded') ORDER BY source,path")
	if err != nil {
		return Gaps{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var source, path, status string
		if err = rows.Scan(&source, &path, &status); err != nil {
			return Gaps{}, err
		}
		items = append(items, corpus.Skipped{Path: filepath.Join(source, filepath.FromSlash(path)), Reason: status})
	}
	return MergeGaps(items), rows.Err()
}
