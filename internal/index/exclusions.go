package index

import (
	"brain/internal/corpus"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"time"
)

type exclusionRemoval struct {
	Current, Total int
	Cleanup        bool
}

func removeExcluded(db *sql.DB, scan corpus.ScanResult, full bool, missing []int64) (exclusionRemoval, error) {
	removed := exclusionRemoval{}
	tx, err := db.Begin()
	if err != nil {
		return removed, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE meta SET value=value WHERE 0"); err != nil {
		return removed, err
	}
	candidates, _ := json.Marshal(missing)
	rows, err := tx.Query("SELECT id,source,path,deleted_at FROM files WHERE deleted_at IS NOT NULL OR id IN (SELECT value FROM json_each(?))", string(candidates))
	if err != nil {
		return removed, err
	}
	files := []int64{}
	for rows.Next() {
		var id int64
		var source, path string
		var deleted sql.NullFloat64
		if err = rows.Scan(&id, &source, &path, &deleted); err != nil {
			rows.Close()
			return removed, err
		}
		if scan.Excluded(filepath.Join(source, filepath.FromSlash(path))) {
			files = append(files, id)
			if !deleted.Valid {
				removed.Current++
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return removed, err
	}
	affected := map[string]bool{}
	wanted, _ := json.Marshal(files)
	rows, err = tx.Query("SELECT chunk FROM places WHERE file IN (SELECT value FROM json_each(?))", string(wanted))
	if err != nil {
		return removed, err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return removed, err
		}
		affected[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return removed, err
	}
	if _, err = tx.Exec("DELETE FROM places WHERE file IN (SELECT value FROM json_each(?))", string(wanted)); err != nil {
		return removed, err
	}
	if _, err = tx.Exec("DELETE FROM files WHERE id IN (SELECT value FROM json_each(?))", string(wanted)); err != nil {
		return removed, err
	}
	if err = settle(tx, affected, map[occurrence]string{}, float64(time.Now().UnixNano())/1e9); err != nil {
		return removed, err
	}
	ids := make([]string, 0, len(affected))
	for id := range affected {
		ids = append(ids, id)
	}
	wanted, _ = json.Marshal(ids)
	if _, err = tx.Exec("DELETE FROM retired WHERE id IN (SELECT value FROM json_each(?))", string(wanted)); err != nil {
		return removed, err
	}
	fingerprint := scan.PolicyFingerprint()
	var previous string
	err = tx.QueryRow("SELECT value FROM meta WHERE key='exclusion_fingerprint'").Scan(&previous)
	if err != nil && err != sql.ErrNoRows {
		return removed, err
	}
	if previous != fingerprint {
		if _, err = tx.Exec("DELETE FROM retired"); err != nil {
			return removed, err
		}
		if _, err = tx.Exec("INSERT OR REPLACE INTO meta(key,value) VALUES('exclusion_fingerprint',?)", fingerprint); err != nil {
			return removed, err
		}
	}
	if full || len(files) > 0 {
		if _, err = tx.Exec("INSERT OR REPLACE INTO meta(key,value) VALUES('exclusion_cleanup_pending','1')"); err != nil {
			return removed, err
		}
	}
	var pending int
	if err = tx.QueryRow("SELECT count(*) FROM meta WHERE key='exclusion_cleanup_pending'").Scan(&pending); err != nil {
		return removed, err
	}
	removed.Cleanup = pending != 0
	removed.Total = len(files)
	return removed, tx.Commit()
}
