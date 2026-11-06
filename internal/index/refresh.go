package index

import (
	"brain/internal/chunk"
	"brain/internal/convert"
	"brain/internal/corpus"
	"brain/internal/platform"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Report struct {
	New, Updated, Removed, Unchanged, Pending, Purged int
	Skipped                                           []corpus.Skipped
}
type manifest struct {
	ID                                                     int64
	Path, Source, Project, Kind, Status, Digest, Namespace string
	Size, Mtime                                            int64
	Version                                                int
	Deleted                                                sql.NullFloat64
}
type work struct {
	Entry                  corpus.Entry
	Old                    *manifest
	Status, Digest, Change string
	Pieces                 []chunk.Piece
	Rules                  []string
	Keep                   bool
}
type occurrence struct {
	file    int64
	ordinal int
}

type Reference struct {
	Source string `json:"source"`
	Path   string `json:"path"`
}

func (reference Reference) Absolute() string {
	return filepath.Join(reference.Source, filepath.FromSlash(reference.Path))
}

func changes(db Reader, found map[string]corpus.Entry, full bool, protected ...func(string) bool) ([]work, int, []int64, error) {
	namespace := "''"
	if needed, err := needsIdentityUpgrade(db); err != nil {
		return nil, 0, nil, err
	} else if !needed {
		namespace = "namespace"
	}
	rows, err := db.Query("SELECT " + namespace + ",id,path,source,project,kind,status,size,mtime_ns,sha256,chunker_version,deleted_at FROM files")
	if err != nil {
		return nil, 0, nil, err
	}
	stored := map[string]*manifest{}
	for rows.Next() {
		item := &manifest{}
		if err = rows.Scan(&item.Namespace, &item.ID, &item.Path, &item.Source, &item.Project, &item.Kind, &item.Status, &item.Size, &item.Mtime, &item.Digest, &item.Version, &item.Deleted); err != nil {
			rows.Close()
			return nil, 0, nil, err
		}
		stored[filepath.Join(item.Source, filepath.FromSlash(item.Path))] = item
	}
	problem := rows.Err()
	rows.Close()
	if problem != nil {
		return nil, 0, nil, problem
	}
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	items, unchanged := []work{}, 0
	for _, name := range names {
		entry, old := found[name], stored[name]
		if full || old == nil || old.Deleted.Valid || old.Status == "unreadable" || old.Namespace != entry.Namespace || old.Source != entry.Source || old.Project != entry.Project || old.Kind != entry.Kind || old.Size != entry.Size || old.Mtime != entry.MtimeNS || old.Version != chunk.Version {
			items = append(items, work{Entry: entry, Old: old})
		} else {
			unchanged++
		}
	}
	missing := []int64{}
	for name, item := range stored {
		if _, ok := found[name]; !ok && !item.Deleted.Valid && (len(protected) == 0 || !protected[0](name)) {
			missing = append(missing, item.ID)
		}
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })
	return items, unchanged, missing, nil
}

// Pending leaves files under an unavailable source in the existing index.
func Pending(db Reader, found map[string]corpus.Entry, unavailable []string) (int, error) {
	items, _, gone, err := changes(db, found, false, func(name string) bool { return under(name, unavailable) })
	return len(items) + len(gone), err
}

func under(name string, roots []string) bool {
	for _, root := range roots {
		root = filepath.Clean(root)
		if name == root || strings.HasPrefix(name, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func read(item work, state string, config corpus.Config, full bool) work {
	item.Status = "current"
	if item.Entry.Superseded {
		item.Status = "superseded"
	}
	item.Pieces = []chunk.Piece{}
	var data []byte
	if item.Entry.Size > config.Options.MaxFileBytes && item.Entry.Kind != "note" {
		item.Status = "oversize"
	} else {
		limit := config.Options.MaxFileBytes
		if item.Entry.Kind == "note" {
			limit = 0
		}
		var err error
		data, err = platform.ReadSource(item.Entry.Path, limit)
		if errors.Is(err, platform.ErrSourceOversize) {
			item.Status = "oversize"
		} else if err != nil {
			item.Status = "unreadable"
		} else {
			digest := sha256.Sum256(data)
			item.Digest = hex.EncodeToString(digest[:])
		}
	}
	alive := item.Old != nil && !item.Old.Deleted.Valid
	if alive && !full && item.Old.Status == "conversion-failed" && item.Old.Digest == item.Digest && item.Old.Version == chunk.Version {
		item.Status = "conversion-failed"
	}
	text := ""
	if item.Status == "current" || item.Status == "superseded" {
		var err error
		text, err = sourceText(state, item.Entry.Path, item.Digest, data, config.Privacy())
		if err != nil {
			item.Status = "conversion-failed"
		}
	}
	item.Rules = markedRules(text)
	same := alive && item.Old.Status == item.Status && item.Old.Digest == item.Digest && item.Old.Version == chunk.Version && item.Old.Project == item.Entry.Project && item.Old.Kind == item.Entry.Kind && item.Old.Namespace == item.Entry.Namespace && item.Old.Source == item.Entry.Source && item.Old.Path == item.Entry.Relative
	if same {
		item.Change = "unchanged"
		item.Keep = !full
		if !full {
			return item
		}
	}
	if item.Status == "current" || item.Status == "superseded" {
		pieces := convert.Chunks(item.Entry.Path, text)
		item.Pieces = make([]chunk.Piece, len(pieces))
		for i, piece := range pieces {
			item.Pieces[i] = chunk.Piece{Heading: strings.Clone(piece.Heading), Text: strings.Clone(piece.Text)}
		}
	}
	if !same {
		wasCurrent := alive && (item.Old.Status == "current" || item.Old.Status == "superseded")
		if !wasCurrent && (item.Status == "current" || item.Status == "superseded") {
			item.Change = "new"
		} else if wasCurrent {
			item.Change = "updated"
		} else {
			item.Change = "unchanged"
		}
	}
	return item
}

func Refresh(db *sql.DB, state string, config corpus.Config, full bool, limit int) (report Report, err error) {
	report.Skipped = []corpus.Skipped{}
	if err = platform.CheckPrivateState(state); err != nil {
		return report, err
	}
	lease, err := platform.PrivacyLock(state, false, true)
	if err != nil {
		return report, err
	}
	defer lease.Close()
	lock, err := platform.Acquire(state, "index", limit == 0)
	if err != nil && !(errors.Is(err, platform.ErrLocked) && limit > 0) {
		return report, err
	}
	if lock != nil {
		defer lock.Close()
	}
	return refreshLocked(db, state, config, full, limit, lock != nil)
}

// RefreshLocked requires the caller to hold the index lock.
func RefreshLocked(db *sql.DB, state string, config corpus.Config, full bool) (Report, error) {
	return refreshLocked(db, state, config, full, 0, true)
}

func refreshLocked(db *sql.DB, state string, config corpus.Config, full bool, limit int, locked bool) (report Report, err error) {
	report.Skipped = []corpus.Skipped{}
	if err = preparePrivacy(db, config.Privacy()); err != nil {
		return report, err
	}
	canonical, err := filepath.EvalSymlinks(state)
	if err != nil {
		return report, err
	}
	config.Sources = append([]corpus.Source{}, config.Sources...)
	for i := range config.Sources {
		if config.Sources[i].Kind == "note" && filepath.Clean(config.Sources[i].Path) == filepath.Join(state, "notes") {
			config.Sources[i].Path = filepath.Join(canonical, "notes")
		}
	}
	scan, err := corpus.ScanWithPolicy(config)
	found, skipped := scan.Entries, scan.Skipped
	for path, entry := range found {
		if config.Privacy().Match(entry.Path) || config.Privacy().Match(entry.Project) {
			delete(found, path)
			skipped = append(skipped, corpus.Skipped{Path: "[protected path]", Reason: "privacy-protected-path"})
		}
	}
	for i := range skipped {
		skipped[i].Path = config.Privacy().Apply(skipped[i].Path).Text
	}
	if err != nil {
		return report, err
	}
	upgrade, err := needsTextUpgrade(db)
	if err != nil {
		return report, err
	}
	roots := []string{}
	for _, gap := range skipped {
		roots = append(roots, gap.Path)
	}
	protected := func(name string) bool { return under(name, roots) && !scan.Excluded(name) }
	pendingItems, unchanged, pendingMissing, err := changes(db, found, full || upgrade, protected)
	if err != nil {
		return report, err
	}
	report.Unchanged, report.Skipped = unchanged, skipped
	defer func() {
		if err == nil {
			var gaps Gaps
			gaps, err = StoredGaps(db)
			report.Skipped = MergeGaps(gaps.Items, report.Skipped).Items
		}
	}()
	if !locked {
		report.Pending = len(pendingItems) + len(pendingMissing)
		return report, nil
	}
	encoded, err := json.Marshal(skipped)
	if err != nil {
		return report, err
	}
	var prior string
	if err = db.QueryRow("SELECT value FROM meta WHERE key='scan_gaps'").Scan(&prior); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return report, err
	}
	idle := limit > 0 && (len(pendingItems)+len(pendingMissing) >= limit || len(pendingItems)+len(pendingMissing) == 0)
	if limit > 0 && len(pendingItems)+len(pendingMissing) >= limit {
		report.Pending = len(pendingItems) + len(pendingMissing)
	}
	if idle && (prior == string(encoded) || prior == "" && len(skipped) == 0) {
		return report, nil
	}
	if _, err = db.Exec("INSERT OR REPLACE INTO meta(key,value) VALUES('scan_gaps',?)", string(encoded)); err != nil {
		return report, err
	}
	if idle {
		return report, nil
	}
	if err = upgradeIdentity(db); err != nil {
		return report, err
	}
	if err = upgradeText(db); err != nil {
		return report, err
	}
	excluded, err := removeExcluded(db, scan, full, pendingMissing)
	if err != nil {
		return report, err
	}
	report.Purged += excluded.Total
	items, unchanged, missing, err := changes(db, found, full, protected)
	if err != nil {
		return report, err
	}
	report.Unchanged = unchanged
	now := float64(time.Now().UnixNano()) / 1e9
	batch, size := []work{}, int64(0)
	for _, item := range items {
		item = read(item, state, config, full)
		switch item.Change {
		case "new":
			report.New++
		case "updated":
			report.Updated++
		default:
			report.Unchanged++
		}
		bytes := retainedBytes(item)
		capacity := cap(batch)
		if len(batch) == capacity {
			capacity = 2*capacity + 1
		}
		spare := int64(capacity-len(batch)-1) * workBytes
		if len(batch) > 0 && size+bytes+spare > BatchBytes {
			if _, err = apply(db, batch, nil, now, 0); err != nil {
				return report, err
			}
			batch = nil
			size = 0
			capacity = 1
		}
		if len(batch) == cap(batch) {
			grown := make([]work, len(batch), capacity)
			copy(grown, batch)
			batch = grown
		}
		batch = append(batch, item)
		size += bytes
	}
	purgeBefore := float64(0)
	if full {
		purgeBefore = now - float64(config.Options.PurgeAfterDays)*86400
	}
	purged, err := apply(db, batch, missing, now, purgeBefore)
	report.Purged += purged
	if err != nil {
		return report, err
	}
	report.Removed = len(missing) + excluded.Current
	if excluded.Cleanup {
		if err = pruneCache(db, state, config.Privacy()); err != nil {
			return report, err
		}
		if _, err = db.Exec("DELETE FROM meta WHERE key='exclusion_cleanup_pending'"); err != nil {
			return report, err
		}
	}
	if report.New+report.Updated+report.Removed+report.Purged > 0 {
		if _, err = db.Exec("INSERT INTO chunk_fts(chunk_fts) VALUES('optimize')"); err != nil {
			return report, err
		}
	}
	gaps, err := StoredGaps(db)
	if err != nil {
		return report, err
	}
	if err = setPrivacy(db, config.Privacy()); err != nil {
		return report, err
	}
	if gaps.Empty {
		if _, err = db.Exec("INSERT OR REPLACE INTO meta(key,value) VALUES('config_fingerprint',?)", Fingerprint(config)); err != nil {
			return report, err
		}
		_, err = db.Exec("INSERT OR REPLACE INTO meta(key,value) VALUES('scanned_at',?)", now)
	}
	return report, err
}

func apply(db *sql.DB, batch []work, missing []int64, now float64, purgeBefore float64) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// Take the writer lock before reading the batch's old rows.
	if _, err = tx.Exec("UPDATE meta SET value=value WHERE 0"); err != nil {
		return 0, err
	}
	affected, texts := map[string]bool{}, map[occurrence]string{}
	oldPlaces := func(file int64) error {
		rows, err := tx.Query("SELECT chunk FROM places WHERE file=?", file)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				return err
			}
			affected[id] = true
		}
		return rows.Err()
	}
	for _, item := range batch {
		entry := item.Entry
		if item.Keep {
			if _, err = tx.Exec("UPDATE files SET size=?,mtime_ns=? WHERE source=? AND path=?", entry.Size, entry.MtimeNS, entry.Source, entry.Relative); err != nil {
				return 0, err
			}
			continue
		}
		var file int64
		if item.Old != nil {
			file = item.Old.ID
			if err = oldPlaces(file); err != nil {
				return 0, err
			}
			if _, err = tx.Exec("DELETE FROM places WHERE file=?", file); err != nil {
				return 0, err
			}
		}
		values := []any{entry.Relative, entry.Source, entry.Project, entry.Kind, item.Status, entry.Size, entry.MtimeNS, item.Digest, chunk.Version, now, entry.Namespace}
		if file == 0 {
			result, err := tx.Exec("INSERT INTO files(path,source,project,kind,status,size,mtime_ns,sha256,chunker_version,indexed_at,namespace) VALUES(?,?,?,?,?,?,?,?,?,?,?)", values...)
			if err != nil {
				return 0, err
			}
			file, err = result.LastInsertId()
			if err != nil {
				return 0, err
			}
		} else {
			if _, err = tx.Exec("UPDATE files SET path=?,source=?,project=?,kind=?,status=?,size=?,mtime_ns=?,sha256=?,chunker_version=?,indexed_at=?,namespace=?,deleted_at=NULL WHERE id=?", append(values, file)...); err != nil {
				return 0, err
			}
		}
		rules, _ := json.Marshal(item.Rules)
		if _, err = tx.Exec("INSERT OR REPLACE INTO redactions(file,rules) VALUES(?,?)", file, string(rules)); err != nil {
			return 0, err
		}
		for ordinal, piece := range item.Pieces {
			id := chunk.ID(chunk.DocumentKey(entry.Namespace, entry.Relative, piece.Text))
			affected[id] = true
			texts[occurrence{file, ordinal}] = piece.Text
			if _, err = tx.Exec("INSERT INTO places(file,ordinal,chunk,heading) VALUES(?,?,?,?)", file, ordinal, id, piece.Heading); err != nil {
				return 0, err
			}
		}
	}
	for _, file := range missing {
		if err = oldPlaces(file); err != nil {
			return 0, err
		}
		if _, err = tx.Exec("UPDATE files SET deleted_at=? WHERE id=? AND deleted_at IS NULL", now, file); err != nil {
			return 0, err
		}
	}
	purged := []int64{}
	if purgeBefore > 0 {
		rows, err := tx.Query("SELECT id FROM files WHERE deleted_at<?", purgeBefore)
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var file int64
			if err = rows.Scan(&file); err != nil {
				rows.Close()
				return 0, err
			}
			purged = append(purged, file)
		}
		problem := rows.Err()
		rows.Close()
		if problem != nil {
			return 0, problem
		}
		for _, file := range purged {
			if err = oldPlaces(file); err != nil {
				return 0, err
			}
			if _, err = tx.Exec("DELETE FROM places WHERE file=?", file); err != nil {
				return 0, err
			}
		}
	}
	if err = settle(tx, affected, texts, now); err != nil {
		return 0, err
	}
	for _, file := range purged {
		if _, err = tx.Exec("DELETE FROM files WHERE id=?", file); err != nil {
			return 0, err
		}
	}
	if purgeBefore > 0 {
		if _, err = tx.Exec("DELETE FROM retired WHERE retired_at<?", purgeBefore); err != nil {
			return 0, err
		}
	}
	return len(purged), tx.Commit()
}

func settle(tx *sql.Tx, affected map[string]bool, texts map[occurrence]string, now float64) error {
	ids := make([]string, 0, len(affected))
	for id := range affected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return nil
	}
	wanted, _ := json.Marshal(ids)
	type holder struct {
		file                                                  int64
		ordinal                                               int
		stored                                                sql.NullString
		deleted, penalty                                      bool
		source, path, heading, project, kind, text, namespace string
	}
	holders := map[string][]holder{}
	rows, err := tx.Query(`SELECT files.namespace,places.chunk,files.id,files.deleted_at IS NOT NULL,files.status='superseded' OR files.kind='third-party',files.source,files.path,places.heading,files.project,files.kind,places.ordinal,places.text,COALESCE(places.text,(SELECT text FROM chunks WHERE id=places.chunk))
 FROM places JOIN files ON files.id=places.file WHERE places.chunk IN (SELECT value FROM json_each(?)) ORDER BY places.ordinal`, string(wanted))
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var item holder
		var oldText sql.NullString
		if err = rows.Scan(&item.namespace, &id, &item.file, &item.deleted, &item.penalty, &item.source, &item.path, &item.heading, &item.project, &item.kind, &item.ordinal, &item.stored, &oldText); err != nil {
			rows.Close()
			return err
		}
		item.text = oldText.String
		if text, found := texts[occurrence{item.file, item.ordinal}]; found {
			item.text = text
		}
		holders[id] = append(holders[id], item)
	}
	problem := rows.Err()
	rows.Close()
	if problem != nil {
		return problem
	}
	for _, id := range ids {
		found := holders[id]
		if len(found) == 0 {
			if _, err = tx.Exec("INSERT OR REPLACE INTO retired(id,heading,retired_at) SELECT id,heading,? FROM chunks WHERE id=?", now, id); err != nil {
				return err
			}
			if _, err = tx.Exec("DELETE FROM chunks WHERE id=?", id); err != nil {
				return err
			}
			continue
		}
		sort.SliceStable(found, func(i, j int) bool {
			a, b := found[i], found[j]
			if a.deleted != b.deleted {
				return !a.deleted
			}
			if a.penalty != b.penalty {
				return !a.penalty
			}
			if len(a.path) != len(b.path) {
				return len(a.path) < len(b.path)
			}
			return a.source+"/"+a.path < b.source+"/"+b.path
		})
		best := found[0]
		others, seen := []Reference{}, map[string]bool{}
		for _, one := range found {
			name := one.source + "/" + one.path
			if !one.deleted && name != best.source+"/"+best.path && !seen[name] {
				others = append(others, Reference{one.source, one.path})
				seen[name] = true
			}
		}
		// NULL shares the canonical text. Preserve differing occurrences before
		// changing that text, including unchanged and soft-deleted copies.
		for _, one := range found {
			variant := sql.NullString{String: one.text, Valid: one.text != best.text}
			if variant.Valid != one.stored.Valid || variant.Valid && variant.String != one.stored.String {
				if _, err = tx.Exec("UPDATE places SET text=? WHERE file=? AND ordinal=?", variant, one.file, one.ordinal); err != nil {
					return err
				}
			}
		}
		also, _ := json.Marshal(others)
		result, err := tx.Exec("UPDATE chunks SET file=?,heading=?,also_in=?,project=?,kind=?,text=?,bytes=? WHERE id=?", best.file, best.heading, string(also), best.project, best.kind, best.text, len(best.text), id)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed == 0 {
			if _, err = tx.Exec("INSERT INTO chunks(id,file,heading,text,bytes,dedupe_key,also_in,project,kind) VALUES(?,?,?,?,?,?,?,?,?)", id, best.file, best.heading, best.text, len(best.text), chunk.DocumentKey(best.namespace, best.path, best.text), string(also), best.project, best.kind); err != nil {
				return err
			}
			if _, err = tx.Exec("DELETE FROM retired WHERE id=?", id); err != nil {
				return err
			}
		}
	}
	return nil
}
