package index

import (
	"brain/internal/platform"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPrivateIndexRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	_, state, db, _ := fixture(t)
	db.Close()
	if err := os.Chmod(state, 0755); err != nil {
		t.Fatal(err)
	}
	for _, readonly := range []bool{false, true} {
		opened, err := Open(state, false, false, readonly)
		if err == nil {
			opened.Close()
			t.Errorf("shared root accepted, readOnly=%v", readonly)
		}
	}
}

func TestPrivateSQLiteFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	_, state, db, _ := fixture(t)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(filepath.Join(state, "index.sqlite"+suffix))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Errorf("SQLite %q permissions = %o", suffix, info.Mode().Perm())
		}
	}
	db.Close()
}

func TestLegacySQLitePrivacy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	for _, readonly := range []bool{false, true} {
		t.Run(map[bool]string{false: "writable", true: "read only"}[readonly], func(t *testing.T) {
			source, state, db, config := fixture(t)
			write(t, source, "keeper.md", "A private keeper lantern.")
			refresh(t, db, state, config, false, 0)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(state, "index.sqlite")
			if err := os.Chmod(path, 0644); err != nil {
				t.Fatal(err)
			}
			if readonly {
				if err := os.Chmod(state, 0500); err != nil {
					t.Fatal(err)
				}
				defer os.Chmod(state, 0700)
			}
			reader, err := OpenRead(state, readonly)
			if err != nil {
				t.Fatal(err)
			}
			var text string
			if err = reader.QueryRow("SELECT text FROM chunks").Scan(&text); err != nil || text != "A private keeper lantern." {
				t.Fatal(text, err)
			}
			if err = reader.Close(); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			want := os.FileMode(0600)
			if readonly {
				want = 0644
			}
			if info.Mode().Perm() != want {
				t.Fatalf("mode=%o, want %o", info.Mode().Perm(), want)
			}
		})
	}
}

func TestSQLiteHardLinkRefused(t *testing.T) {
	_, state, db, _ := fixture(t)
	db.Close()
	path := filepath.Join(state, "index.sqlite")
	outside := filepath.Join(t.TempDir(), "linked.sqlite")
	if err := os.Link(path, outside); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(outside)
	if err != nil {
		t.Fatal(err)
	}
	for _, rebuild := range []bool{false, true} {
		opened, err := Open(state, true, rebuild, false)
		if err == nil {
			opened.Close()
			t.Fatal("hard-linked database accepted")
		}
	}
	after, err := os.Stat(outside)
	if err != nil {
		t.Fatal(err)
	}
	if before.Mode() != after.Mode() || before.Size() != after.Size() {
		t.Fatal("outside file changed")
	}
}

func TestPrivateCorruptRebuild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	_, state, db, _ := fixture(t)
	db.Close()
	if err := os.WriteFile(filepath.Join(state, "index.sqlite"), []byte("corrupt test input"), 0600); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := Open(state, true, true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer rebuilt.Close()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(filepath.Join(state, "index.sqlite"+suffix))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal(suffix, info, err)
		}
	}
}

func TestUnsafeStoredRoot(t *testing.T) {
	_, state, db, _ := fixture(t)
	db.Close()
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(state, link); err != nil {
		t.Fatal(err)
	}
	if opened, err := OpenStored(link); err == nil {
		opened.Close()
		t.Fatal("linked root read")
	}
	if opened, err := orphanSnapshot(link); err == nil {
		opened.Close()
		t.Fatal("linked root snapshot")
	}
}

func TestLiveSQLiteFilesBecomePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	_, state, db, _ := fixture(t)
	defer db.Close()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Chmod(filepath.Join(state, "index.sqlite"+suffix), 0644); err != nil {
			t.Fatal(err)
		}
	}
	reader, err := OpenRead(state, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(filepath.Join(state, "index.sqlite"+suffix))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal(suffix, info, err)
		}
	}
}

func TestSQLiteSidecarHardLinksRefused(t *testing.T) {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		t.Run(suffix, func(t *testing.T) {
			_, state, db, _ := fixture(t)
			db.Close()
			outside := filepath.Join(t.TempDir(), "outside")
			if err := os.WriteFile(outside, []byte("outside bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(outside, filepath.Join(state, "index.sqlite"+suffix)); err != nil {
				t.Fatal(err)
			}
			if opened, err := Open(state, true, true, false); err == nil {
				opened.Close()
				t.Fatal("linked sidecar accepted")
			}
			data, err := os.ReadFile(outside)
			if err != nil || string(data) != "outside bytes" {
				t.Fatal(string(data), err)
			}
		})
	}
}

func TestReadOnlySQLiteHardLinksRefused(t *testing.T) {
	readers := []struct {
		name string
		open func(string) error
	}{
		{"Open", func(state string) error {
			db, err := Open(state, false, false, true)
			if db != nil {
				db.Close()
			}
			return err
		}},
		{"OpenRead", func(state string) error {
			db, err := OpenRead(state, true)
			if db != nil {
				db.Close()
			}
			return err
		}},
		{"OpenStored", func(state string) error {
			db, err := OpenStored(state)
			if db != nil {
				db.Close()
			}
			return err
		}},
		{"snapshot", func(state string) error {
			db, err := orphanSnapshot(state)
			if db != nil {
				db.Close()
			}
			return err
		}},
	}
	for _, reader := range readers {
		for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
			t.Run(reader.name+"/index.sqlite"+suffix, func(t *testing.T) {
				_, state, writer, _ := fixture(t)
				defer writer.Close()
				if _, err := writer.Exec("INSERT INTO meta(key,value) VALUES('read-only-test',1)"); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(state, "index.sqlite"+suffix)
				if suffix == "-journal" {
					if err := os.WriteFile(path, nil, 0600); err != nil {
						t.Fatal(err)
					}
				}
				outside := filepath.Join(t.TempDir(), "outside.sqlite")
				if err := os.Link(path, outside); err != nil {
					t.Fatal(err)
				}
				before, err := os.ReadFile(outside)
				if err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(outside)
				if err != nil {
					t.Fatal(err)
				}
				if runtime.GOOS != "windows" {
					if err := os.Chmod(state, 0500); err != nil {
						t.Fatal(err)
					}
					defer os.Chmod(state, 0700)
				}
				if err = reader.open(state); !errors.Is(err, platform.ErrStateUnsafe) {
					t.Errorf("read-only linked %q error=%v, want unsafe state", suffix, err)
				}
				after, err := os.ReadFile(outside)
				if err != nil {
					t.Fatal(err)
				}
				afterInfo, err := os.Stat(outside)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) || info.Mode() != afterInfo.Mode() {
					t.Fatal("read-only opening changed outside bytes or mode")
				}
			})
		}
	}
}
