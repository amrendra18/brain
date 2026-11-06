package index

import (
	"brain/internal/platform"
	"database/sql"
	"errors"
	"fmt"
	"modernc.org/sqlite"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var ErrMissing = errors.New("the index is missing or unusable")
var ErrOldSQLite = errors.New("SQLite 3.43.0 or newer is required")

const Schema = 1
const BatchBytes = 4000000
const FTSCheck = "INSERT INTO chunk_fts (chunk_fts, rank) VALUES ('integrity-check', 1)"

type Reader interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS files (
 id INTEGER PRIMARY KEY, path TEXT NOT NULL, source TEXT NOT NULL, project TEXT NOT NULL, kind TEXT NOT NULL,
 status TEXT NOT NULL, size INTEGER NOT NULL, mtime_ns INTEGER NOT NULL, sha256 TEXT NOT NULL,
 namespace TEXT NOT NULL DEFAULT '', chunker_version INTEGER NOT NULL, indexed_at REAL NOT NULL, deleted_at REAL, UNIQUE(source,path));
CREATE TABLE IF NOT EXISTS redactions(file INTEGER PRIMARY KEY,rules TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS places (file INTEGER NOT NULL,ordinal INTEGER NOT NULL,chunk TEXT NOT NULL,heading TEXT NOT NULL,text TEXT,PRIMARY KEY(file,ordinal)) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS places_chunk ON places(chunk);
CREATE TABLE IF NOT EXISTS chunks (
 row INTEGER PRIMARY KEY,id TEXT NOT NULL UNIQUE,file INTEGER NOT NULL,heading TEXT NOT NULL,text TEXT NOT NULL,
 bytes INTEGER NOT NULL,dedupe_key TEXT NOT NULL,also_in TEXT NOT NULL,project TEXT NOT NULL,kind TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS chunks_file ON chunks(file);
CREATE TABLE IF NOT EXISTS retired (id TEXT PRIMARY KEY,heading TEXT NOT NULL,retired_at REAL NOT NULL) WITHOUT ROWID;
CREATE VIRTUAL TABLE IF NOT EXISTS chunk_fts USING fts5(heading,text,project,kind,content='chunks',content_rowid='row',tokenize='porter unicode61');
CREATE TRIGGER IF NOT EXISTS chunks_insert AFTER INSERT ON chunks BEGIN
 INSERT INTO chunk_fts(rowid,heading,text,project,kind) VALUES(new.row,new.heading,new.text,new.project,new.kind);
END;
CREATE TRIGGER IF NOT EXISTS chunks_delete AFTER DELETE ON chunks BEGIN
 INSERT INTO chunk_fts(chunk_fts,rowid,heading,text,project,kind) VALUES('delete',old.row,old.heading,old.text,old.project,old.kind);
END;
` + chunkUpdateSQL + `
INSERT OR IGNORE INTO meta(key,value) VALUES('schema',1);
`

const chunkUpdateSQL = `CREATE TRIGGER IF NOT EXISTS chunks_update AFTER UPDATE ON chunks
 WHEN old.text IS NOT new.text OR old.heading IS NOT new.heading OR old.project IS NOT new.project OR old.kind IS NOT new.kind BEGIN
 INSERT INTO chunk_fts(chunk_fts,rowid,heading,text,project,kind) VALUES('delete',old.row,old.heading,old.text,old.project,old.kind);
 INSERT INTO chunk_fts(rowid,heading,text,project,kind) VALUES(new.row,new.heading,new.text,new.project,new.kind);
END;
`

func VersionOK(version string) bool {
	var major, minor, patch int
	if _, err := fmt.Sscanf(version, "%d.%d.%d", &major, &minor, &patch); err != nil {
		return false
	}
	return major > 3 || major == 3 && minor >= 43
}
func Locked(err error) bool {
	return err != nil && (strings.Contains(strings.ToLower(err.Error()), "locked") || strings.Contains(strings.ToLower(err.Error()), "busy"))
}
func Rebuildable(err error) bool {
	const sqliteCorrupt, sqliteNotADB = 11, 26
	var engine *sqlite.Error
	if errors.As(err, &engine) {
		code := engine.Code() & 0xff
		return code == sqliteCorrupt || code == sqliteNotADB
	}
	return errors.Is(err, ErrMissing)
}
func Open(state string, create, rebuild, readOnly bool) (*sql.DB, error) {
	if err := checkDatabase(state); err != nil {
		return nil, err
	}
	if readOnly && orphanWAL(state) {
		return orphanSnapshot(state)
	}
	return open(state, create, rebuild, readOnly, false, nil)
}

// OpenLocked requires the caller to hold the index lock through refresh and close.
func OpenLocked(state string, rebuild bool) (*sql.DB, error) {
	if err := checkDatabase(state); err != nil {
		return nil, err
	}
	return open(state, true, rebuild, false, false, nil, true)
}

type Stored struct {
	*sql.DB
	lock *platform.Lock
}

func (stored *Stored) Close() error {
	err := stored.DB.Close()
	if stored.lock != nil {
		if unlocked := stored.lock.Close(); err == nil {
			err = unlocked
		}
		stored.lock = nil
	}
	return err
}

// OpenStored keeps an immutable file protected through its last read and close.
func OpenStored(state string) (*Stored, error) {
	return openReader(state, true, true)
}

// OpenRead retains any immutable fallback guard until the database closes.
func OpenRead(state string, readOnly bool) (*Stored, error) {
	return openReader(state, readOnly, false)
}

func openReader(state string, readOnly, snapshot bool) (*Stored, error) {
	if err := checkDatabase(state); err != nil {
		return nil, err
	}
	stored := &Stored{}
	if readOnly && orphanWAL(state) {
		db, err := orphanSnapshot(state)
		if err != nil {
			return nil, err
		}
		stored.DB = db
		return stored, nil
	}
	protect := func() error {
		if stored.lock != nil {
			return nil
		}
		lock, err := platform.ReadLock(state)
		if err != nil {
			return err
		}
		stored.lock = lock
		wal, err := os.Stat(filepath.Join(state, "index.sqlite-wal"))
		if err == nil && wal.Size() > 0 {
			return platform.ErrLocked
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	db, err := open(state, false, false, readOnly, snapshot, protect)
	if err != nil {
		if stored.lock != nil {
			stored.lock.Close()
		}
		return nil, err
	}
	stored.DB = db
	return stored, nil
}

func orphanWAL(state string) bool {
	wal, err := os.Stat(filepath.Join(state, "index.sqlite-wal"))
	if err == nil && wal.Size() > 0 {
		if _, err := os.Lstat(filepath.Join(state, "index.sqlite-shm")); errors.Is(err, os.ErrNotExist) {
			return true
		}
	}
	return false
}

func open(state string, create, rebuild, readOnly, stored bool, protect func() error, held ...bool) (*sql.DB, error) {
	if !readOnly {
		lock, err := prepareDatabase(state, create, rebuild, held...)
		if err != nil {
			return nil, err
		}
		if lock != nil {
			defer lock.Close()
		}
	}
	path := filepath.Join(state, "index.sqlite")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) && !create {
		return nil, ErrMissing
	}
	open := func(immutable bool) (*sql.DB, error) {
		if immutable && protect != nil {
			if err := protect(); err != nil {
				return nil, err
			}
		}
		mode := "rw"
		if create {
			mode = "rwc"
		}
		if readOnly {
			mode = "ro"
		}
		escaped := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(filepath.ToSlash(path))
		uri := "file:" + escaped + "?mode=" + mode + "&_pragma=busy_timeout(5000)&_pragma=recursive_triggers(1)"
		// This fallback is safe only when there are no committed WAL pages.
		if immutable {
			uri += "&immutable=1"
		}
		db, err := sql.Open("sqlite", uri)
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(1)
		fail := func(err error) (*sql.DB, error) { db.Close(); return nil, err }
		var version string
		if err = db.QueryRow("SELECT sqlite_version()").Scan(&version); err != nil {
			return fail(err)
		}
		if !VersionOK(version) {
			return fail(ErrOldSQLite)
		}
		if create && !readOnly {
			if _, err = db.Exec("PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL;" + schemaSQL); err != nil {
				return fail(err)
			}
		}
		var schema string
		if err = db.QueryRow("SELECT name FROM sqlite_schema WHERE type='table' AND name='meta'").Scan(&schema); errors.Is(err, sql.ErrNoRows) {
			return fail(ErrMissing)
		} else if err != nil {
			return fail(err)
		}
		err = db.QueryRow("SELECT value FROM meta WHERE key='schema'").Scan(&schema)
		if errors.Is(err, sql.ErrNoRows) || err == nil && schema != strconv.Itoa(Schema) {
			return fail(ErrMissing)
		} else if err != nil {
			return fail(err)
		}
		if rebuild {
			var status string
			if err = db.QueryRow("PRAGMA quick_check").Scan(&status); err != nil {
				return fail(err)
			}
			if status != "ok" {
				return fail(ErrMissing)
			}
			if _, err = db.Exec(FTSCheck); err != nil {
				return fail(err)
			}
		}
		return db, nil
	}
	immutable := false
	if stored {
		wal, problem := os.Stat(path + "-wal")
		immutable = errors.Is(problem, os.ErrNotExist) || problem == nil && wal.Size() == 0
	}
	db, err := open(immutable)
	if err != nil && readOnly {
		wal, problem := os.Stat(path + "-wal")
		if errors.Is(problem, os.ErrNotExist) || problem == nil && wal.Size() == 0 {
			return open(true)
		}
	}
	if err == nil || !rebuild || readOnly || !Rebuildable(err) {
		return db, err
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if problem := os.Remove(path + suffix); problem != nil && !errors.Is(problem, os.ErrNotExist) {
			return nil, problem
		}
	}
	if err = platform.PreparePrivateFile(state, "index.sqlite", true); err != nil {
		return nil, err
	}
	db, err = open(false)
	return db, err
}
func ScannedAt(db Reader) (float64, error) {
	var scanned float64
	err := db.QueryRow("SELECT value FROM meta WHERE key='scanned_at'").Scan(&scanned)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return scanned, err
}
func Totals(db Reader) (files, chunks, folded int, err error) {
	if err = db.QueryRow("SELECT count(*) FROM files WHERE deleted_at IS NULL AND status IN ('current','superseded')").Scan(&files); err != nil {
		return
	}
	if err = db.QueryRow("SELECT count(*) FROM chunks JOIN files ON files.id=chunks.file WHERE files.deleted_at IS NULL").Scan(&chunks); err != nil {
		return
	}
	var places int
	err = db.QueryRow("SELECT count(DISTINCT places.file||'/'||places.chunk) FROM places JOIN files ON files.id=places.file WHERE files.deleted_at IS NULL").Scan(&places)
	folded = places - chunks
	return
}
func Bytes(state string) int64 {
	var size int64
	for _, suffix := range []string{"", "-wal"} {
		if info, err := os.Stat(filepath.Join(state, "index.sqlite") + suffix); err == nil {
			size += info.Size()
		}
	}
	return size
}
