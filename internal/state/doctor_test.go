package state

import (
	"brain/internal/index"
	"brain/tests/testutil"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDoctorChecks(t *testing.T) {
	source, folder, config, db := fixture(t)
	config.Sources = config.Sources[:1]
	data, _ := json.Marshal(config)
	os.WriteFile(filepath.Join(folder, "config.json"), data, 0600)
	inspect := func(want string) {
		t.Helper()
		problems, checks, err := Doctor(folder, "sh")
		if err != nil {
			t.Fatal(err)
		}
		if want == "" {
			if len(problems) != 0 || len(checks) != 19 {
				t.Fatal(problems, checks)
			}
		} else {
			found := false
			for _, problem := range problems {
				if problem.Code == want {
					found = true
				}
				if strings.Contains(problem.Message, folder) {
					t.Fatal("absolute path in message", problem)
				}
			}
			if !found {
				t.Fatal(want, problems, checks)
			}
		}
	}
	inspect("")
	os.Rename(source, source+"-moved")
	inspect("")
	os.Rename(source+"-moved", source)
	config.Options.InlineRefreshLimit = 1
	data, _ = json.Marshal(config)
	os.WriteFile(filepath.Join(folder, "config.json"), data, 0600)
	os.WriteFile(filepath.Join(source, "alpha/PLAN.md"), []byte("new document"), 0600)
	inspect("index-stale")
	if _, err := index.Refresh(db, folder, config, false, 0); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(source, "beta/PLAN.md"))
	index.Refresh(db, folder, config, false, 0)
	db.Exec("UPDATE files SET deleted_at=deleted_at-8*86400 WHERE deleted_at IS NOT NULL")
	inspect("purge-overdue")
	db.Exec("DELETE FROM meta WHERE key='scanned_at'")
	inspect("index-missing")
	db.Close()
	os.WriteFile(filepath.Join(folder, "index.sqlite"), []byte("corrupt database"), 0600)
	inspect("index-damaged")
	os.Remove(filepath.Join(folder, "index.sqlite"))
	inspect("index-missing")
	os.WriteFile(filepath.Join(folder, "config.json"), []byte("bad JSON"), 0600)
	inspect("config-invalid")
	testutil.Permissions(t, folder, 0500)
	inspect("state-unwritable")
	testutil.Permissions(t, folder, 0700)
	problems, _, err := Doctor(filepath.Join(t.TempDir(), "absent"), "sh")
	if err != nil || len(problems) != 1 || problems[0].Code != "state-uninitialised" {
		t.Fatal(problems, err)
	}
	problems, _, err = Doctor("//server/share/state", "sh")
	if err != nil || len(problems) != 1 || problems[0].Code != "state-remote" {
		t.Fatal(problems, err)
	}
}

func TestDoctorUnsafeStateDoesNotProbe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode check")
	}
	folder := t.TempDir()
	testutil.Permissions(t, folder, 0755)
	problems, checks, err := Doctor(folder, "sh")
	if err != nil || len(problems) != 1 || problems[0].Code != "state-unsafe" {
		t.Fatal(problems, checks, err)
	}
	for _, check := range checks {
		if check.Name == "state-writable" || check.Name == "source" || check.Name == "index" {
			t.Fatal("unsafe state reached later checks", check)
		}
	}
	files, err := os.ReadDir(folder)
	if err != nil || len(files) != 0 {
		t.Fatal(files, err)
	}
	info, err := os.Stat(folder)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("doctor changed permissions", info, err)
	}
}

func TestDoctorLinkedDatabase(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		_, folder, _, db := fixture(t)
		db.Close()
		outside := filepath.Join(t.TempDir(), "outside.sqlite")
		if err := os.Link(filepath.Join(folder, "index.sqlite"), outside); err != nil {
			t.Fatal(err)
		}
		if readOnly {
			if runtime.GOOS == "windows" {
				continue
			}
			testutil.Permissions(t, folder, 0500)
			defer os.Chmod(folder, 0700)
		}
		problems, _, err := Doctor(folder, "sh")
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, problem := range problems {
			if problem.Code == "index-damaged" {
				t.Fatal("unsafe file recommends rebuilding", problem)
			}
			found = found || problem.Code == "state-unsafe"
		}
		if !found {
			t.Fatal("unsafe index was not diagnosed", problems)
		}
	}
}
func TestDoctorFTSDamage(t *testing.T) {
	_, folder, config, db := fixture(t)
	config.Sources = config.Sources[:1]
	data, _ := json.Marshal(config)
	os.WriteFile(filepath.Join(folder, "config.json"), data, 0600)
	if _, err := db.Exec("DROP TRIGGER chunks_delete;DELETE FROM chunks WHERE row=(SELECT min(row) FROM chunks)"); err != nil {
		t.Fatal(err)
	}
	problems, checks, err := Doctor(folder, "sh")
	if err != nil || len(problems) != 1 || problems[0].Code != "index-damaged" {
		t.Fatal(problems, checks, err)
	}
	if checks[len(checks)-1].Name != "fts-integrity" || checks[len(checks)-1].OK {
		t.Fatal(checks)
	}
}
