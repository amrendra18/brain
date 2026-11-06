package index

import (
	"brain/internal/platform"
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"strconv"
)

const snapshotLimit = 256 * 1024 * 1024

// orphanSnapshot never recreates a lost shared-memory file. It copies committed
// pages into memory while the existing lock excludes Brain's index writers.
func orphanSnapshot(state string) (*sql.DB, error) {
	if err := checkDatabase(state); err != nil {
		return nil, err
	}
	lock, err := platform.ReadLock(state)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	identities := map[string]os.FileInfo{}
	read := func(name string) ([]byte, error) {
		if err := platform.SafeStatePath(state, name); err != nil {
			return nil, err
		}
		root, err := os.OpenRoot(state)
		if err != nil {
			return nil, err
		}
		defer root.Close()
		info, err := root.Lstat(name)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() > snapshotLimit {
			return nil, ErrMissing
		}
		file, err := root.Open(name)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		opened, err := file.Stat()
		if err != nil || !os.SameFile(info, opened) {
			return nil, ErrMissing
		}
		identities[name] = opened
		data, err := io.ReadAll(io.LimitReader(file, snapshotLimit+1))
		if len(data) > snapshotLimit {
			return nil, ErrMissing
		}
		return data, err
	}
	database, err := read("index.sqlite")
	if err != nil {
		return nil, err
	}
	wal, err := read("index.sqlite-wal")
	if errors.Is(err, os.ErrNotExist) {
		return nil, platform.ErrLocked
	}
	if err != nil {
		return nil, err
	}
	if len(database)+len(wal) > snapshotLimit {
		return nil, ErrMissing
	}
	root, err := os.OpenRoot(state)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	for name, before := range identities {
		after, err := root.Lstat(name)
		if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
			return nil, platform.ErrLocked
		}
	}
	data, err := committedPages(database, wal)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	connection, err := db.Conn(context.Background())
	if err == nil {
		err = connection.Raw(func(raw any) error {
			return raw.(interface{ Deserialize([]byte) error }).Deserialize(data)
		})
		connection.Close()
	}
	if err == nil {
		_, err = db.Exec("PRAGMA query_only=1; PRAGMA temp_store=MEMORY")
	}
	if err == nil {
		var schema, status string
		err = db.QueryRow("SELECT value FROM meta WHERE key='schema'").Scan(&schema)
		if err == nil {
			err = db.QueryRow("PRAGMA quick_check").Scan(&status)
		}
		if err == nil && (schema != strconv.Itoa(Schema) || status != "ok") {
			err = ErrMissing
		}
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// SQLite's WAL format records a rolling checksum and a page count at each
// commit. Invalid or incomplete tail frames cannot become committed pages.
// Format: https://www.sqlite.org/fileformat2.html#wal_file_format
func committedPages(database, wal []byte) ([]byte, error) {
	if len(database) < 100 || !bytes.Equal(database[:16], []byte("SQLite format 3\x00")) || len(wal) < 32 {
		return nil, ErrMissing
	}
	if database[18] < 1 || database[18] > 2 || database[19] < 1 || database[19] > 2 {
		return nil, ErrMissing
	}
	big := binary.BigEndian
	magic := big.Uint32(wal[:4])
	if magic != 0x377f0682 && magic != 0x377f0683 || big.Uint32(wal[4:8]) != 3007000 {
		return nil, ErrMissing
	}
	pageSize := int(big.Uint32(wal[8:12]))
	dbSize := int(big.Uint16(database[16:18]))
	if dbSize == 1 {
		dbSize = 65536
	}
	if pageSize < 512 || pageSize > 65536 || pageSize&(pageSize-1) != 0 || dbSize != pageSize || len(database)%pageSize != 0 {
		return nil, ErrMissing
	}
	var order binary.ByteOrder = binary.LittleEndian
	if magic == 0x377f0683 {
		order = big
	}
	var a, b uint32
	checksum := func(data []byte) {
		for offset := 0; offset < len(data); offset += 8 {
			a += order.Uint32(data[offset:offset+4]) + b
			b += order.Uint32(data[offset+4:offset+8]) + a
		}
	}
	checksum(wal[:24])
	if a != big.Uint32(wal[24:28]) || b != big.Uint32(wal[28:32]) {
		return nil, ErrMissing
	}
	end, pages := 32, 0
	for offset := 32; offset+24+pageSize <= len(wal); offset += 24 + pageSize {
		frame := wal[offset : offset+24+pageSize]
		if !bytes.Equal(frame[8:16], wal[16:24]) {
			break
		}
		checksum(frame[:8])
		checksum(frame[24:])
		if a != big.Uint32(frame[16:20]) || b != big.Uint32(frame[20:24]) {
			break
		}
		page := big.Uint32(frame[:4])
		count := big.Uint32(frame[4:8])
		if page == 0 || uint64(page)*uint64(pageSize) > snapshotLimit || uint64(count)*uint64(pageSize) > snapshotLimit {
			return nil, ErrMissing
		}
		if count != 0 {
			end, pages = offset+len(frame), int(count)
		}
	}
	if pages == 0 {
		result := bytes.Clone(database)
		result[18], result[19] = 1, 1
		return result, nil
	}
	result := make([]byte, pages*pageSize)
	copy(result, database)
	for offset := 32; offset < end; offset += 24 + pageSize {
		page := int(big.Uint32(wal[offset : offset+4]))
		if page <= pages {
			copy(result[(page-1)*pageSize:page*pageSize], wal[offset+24:offset+24+pageSize])
		}
	}
	// A private deserialized database uses rollback format, with no journal file.
	if !bytes.Equal(result[:16], []byte("SQLite format 3\x00")) || result[18] < 1 || result[18] > 2 || result[19] < 1 || result[19] > 2 {
		return nil, ErrMissing
	}
	result[18], result[19] = 1, 1
	return result, nil
}
