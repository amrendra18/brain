package search

import (
	"brain/internal/corpus"
	"brain/internal/index"
	"brain/internal/query"
	"database/sql"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func penaltyFixture(t *testing.T) (*sql.DB, string, string, corpus.Config, func(string, string)) {
	t.Helper()
	base := t.TempDir()
	source, state := filepath.Join(base, "corpus"), filepath.Join(base, "state")
	os.Mkdir(source, 0700)
	os.Mkdir(state, 0700)
	put := func(name, text string) {
		t.Helper()
		path := filepath.Join(source, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 6; i++ {
		put(fmt.Sprintf("other/file%d.md", i), fmt.Sprintf("# Other %d\n\nRope and canvas, item %d.", i, i))
	}
	db, err := index.Open(state, true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, source, state, corpus.Default(source, []string{source}, nil), put
}

func TestSupersededPenalty(t *testing.T) {
	db, source, state, config, put := penaltyFixture(t)
	const old = "# Harbour plan\n\nThe harbour lease rule keeps boats moored, old."
	put("plan/superseded/harbour.md", old)
	put("plan/Archive/older.md", old+"x")
	if _, err := index.Refresh(db, state, config, false, 0); err != nil {
		t.Fatal(err)
	}
	put("plan/harbour.md", "# Harbour plan\n\nThe harbour lease rule keeps boats moored, new.")
	if _, err := index.Refresh(db, state, config, false, 0); err != nil {
		t.Fatal(err)
	}
	found, err := Find(db, query.Make("harbour lease rule", nil), "", "", 2, false)
	if err != nil || found.Total != 3 || found.Results[0].Path != filepath.Join(source, "plan", "harbour.md") || found.Results[1].Path != filepath.Join(source, "plan", "superseded", "harbour.md") {
		t.Fatal(found, err)
	}
	if ratio := found.Results[1].Score / found.Results[0].Score; math.Abs(ratio-.5) > .02 {
		t.Fatal("superseded score multiplier", ratio)
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM files WHERE status='superseded'").Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	put("plan/superseded/harbour.md", "# Harbour plan\n\nAn older still harbour rule.")
	if _, err = index.Refresh(db, state, config, false, 0); err != nil {
		t.Fatal(err)
	}
	found, err = Find(db, query.Make("older still", nil), "", "", 8, false)
	if err != nil || found.Count != 1 {
		t.Fatal(found, err)
	}
}

func TestThirdPartyOffByDefault(t *testing.T) {
	db, source, state, config, put := penaltyFixture(t)
	config.ThirdParty = []string{"external/*-main"}
	const foreign = "# Tool\n\nShamir secret split in the download tool."
	put("my-tools/README.md", "# Tool\n\nShamir secret split in the borrowed tool.")
	put("external/tool-main/README.md", foreign)
	if report, err := index.Refresh(db, state, config, false, 0); err != nil || report.New != 7 {
		t.Fatal(report, err)
	}
	found, err := Find(db, query.Make("shamir secret split", nil), "", "", 8, false)
	if err != nil || found.Count != 1 {
		t.Fatal(found, err)
	}
	config.Include = []string{"external/*-main"}
	if report, err := index.Refresh(db, state, config, false, 0); err != nil || report.New != 1 {
		t.Fatal(report, err)
	}
	found, err = Find(db, query.Make("shamir secret split", nil), "", "", 8, false)
	if err != nil || found.Count != 2 || found.Results[0].Path != filepath.Join(source, "my-tools", "README.md") || found.Results[1].Kind != "third-party" {
		t.Fatal(found, err)
	}
	if ratio := found.Results[1].Score / found.Results[0].Score; math.Abs(ratio-.5) > .02 {
		t.Fatal("third-party score multiplier", ratio)
	}
	put("my-tools/copy.md", foreign)
	if _, err = index.Refresh(db, state, config, false, 0); err != nil {
		t.Fatal(err)
	}
	found, err = Find(db, query.Make("download tool", nil), "", "", 8, false)
	if err != nil || found.Count != 2 || found.Results[0].Path != filepath.Join(source, "my-tools", "copy.md") || len(found.Results[0].AlsoIn) != 0 || found.Results[1].Kind != "third-party" {
		t.Fatal(found, err)
	}
	config.Include = nil
	if report, err := index.Refresh(db, state, config, false, 0); err != nil || report.Removed != 1 {
		t.Fatal(report, err)
	}
	found, err = Find(db, query.Make("download tool", nil), "", "", 8, false)
	if err != nil || found.Count != 1 || len(found.Results[0].AlsoIn) != 0 {
		t.Fatal(found, err)
	}
	found, err = Find(db, query.Make("shamir", nil), "", "third-party", 8, false)
	if err != nil || found.Count != 0 {
		t.Fatal(found, err)
	}
}
