package evaluaterun

import (
	"brain/internal/index"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
)

// ComparisonOptions names only owned research paths. StateRoot must not exist.
type ComparisonOptions struct {
	ConfigPath, StateRoot, BinaryPath, KeywordPath, SentencePath, FloorPath, CorpusRoot string
	afterFirstCapture                                                                   func()
	beforeScoring                                                                       func()
	issueCommand                                                                        func(*driver, context.Context, ...string) commandRow
}

// ComparisonRank records the complete lexical chunk order, including aliases.
type ComparisonRank struct {
	ID     string   `json:"id"`
	Path   string   `json:"path"`
	AlsoIn []string `json:"also_in"`
	Score  float64  `json:"score"`
}

// ComparisonCommand is the delivered command observation for one query.
type ComparisonCommand = commandRow

// ComparisonRow keeps expected owners separate from the ranker's input.
type ComparisonRow struct {
	Set           string            `json:"set"`
	ID            string            `json:"id"`
	Query         string            `json:"query"`
	Owners        []string          `json:"owners"`
	ExactName     bool              `json:"exact_name"`
	Mode          string            `json:"mode"`
	AsRun         string            `json:"as_run"`
	Total         int               `json:"total"`
	Status        string            `json:"status"`
	Hit           *bool             `json:"hit"`
	Ranks         []ComparisonRank  `json:"ranks"`
	Files         []string          `json:"files"`
	TopThree      []string          `json:"top_three"`
	PublicCommand ComparisonCommand `json:"public_command"`
}

// Baseline is the sealed, two-index lexical side of a comparison. Inventory
// is reconstructed from the hash-checked state during read-only validation.
type Baseline struct {
	SchemaVersion      int                    `json:"schema_version"`
	Status             string                 `json:"status"`
	Failure            string                 `json:"failure,omitempty"`
	Repo               string                 `json:"repo"`
	RecordPath         string                 `json:"-"`
	RecordSHA256       string                 `json:"-"`
	CorpusRoot         string                 `json:"corpus_root"`
	LexicalState       string                 `json:"lexical_state"`
	HybridState        string                 `json:"hybrid_state"`
	LexicalDatabaseSHA string                 `json:"lexical_database_sha256"`
	HybridDatabaseSHA  string                 `json:"hybrid_database_sha256"`
	InventorySHA256    string                 `json:"inventory_sha256"`
	ManifestSHA256     string                 `json:"manifest_sha256"`
	OutcomesSHA256     string                 `json:"outcomes_sha256"`
	KeywordSHA256      string                 `json:"keyword_sha256"`
	SentenceSHA256     string                 `json:"sentence_sha256"`
	ConfigSHA256       string                 `json:"config_sha256"`
	KeywordSnapshot    []byte                 `json:"keyword_snapshot"`
	SentenceSnapshot   []byte                 `json:"sentence_snapshot"`
	ConfigSnapshot     []byte                 `json:"config_snapshot"`
	HybridCopyValid    bool                   `json:"-"`
	FloorPath          string                 `json:"floor_path"`
	FloorSHA256        string                 `json:"floor_sha256"`
	FloorMinimum       int                    `json:"floor_minimum"`
	KeywordHits        int                    `json:"keyword_hits"`
	KeywordFloorMet    bool                   `json:"keyword_floor_met"`
	KeywordRecordPath  string                 `json:"keyword_record_path"`
	KeywordRecordSHA   string                 `json:"keyword_record_sha256"`
	SecondRecordPath   string                 `json:"second_record_path"`
	SecondRecordSHA    string                 `json:"second_record_sha256"`
	BinarySHA256       string                 `json:"binary_sha256"`
	AdapterSHA256      string                 `json:"adapter_sha256"`
	IndexChunks        int                    `json:"index_chunks"`
	Rows               []ComparisonRow        `json:"rows"`
	SecondRows         []ComparisonRow        `json:"second_rows,omitempty"`
	Inventory          []index.InventoryChunk `json:"-"`
}

func fillComparisonOptions(repo string, opt ComparisonOptions) ComparisonOptions {
	if opt.BinaryPath == "" {
		opt.BinaryPath = filepath.Join(repo, ".local", "bin", "brain")
		if runtime.GOOS == "windows" {
			opt.BinaryPath += ".exe"
		}
	}
	return opt
}

func sameRankRows(a, b []ComparisonRow) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		left, right := a[i], b[i]
		left.PublicCommand, right.PublicCommand = commandRow{}, commandRow{}
		if !reflect.DeepEqual(left, right) {
			return false
		}
	}
	return true
}

func sealedDatabaseSHA(state, repo string) (string, error) {
	path := filepath.Join(state, "index.sqlite")
	if _, err := checkedPath(path, repo); err != nil {
		return "", err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		info, err := os.Lstat(path + suffix)
		if err == nil && (suffix == "-wal" && info.Size() > 0 || !info.Mode().IsRegular()) {
			return "", errors.New("index has an unsealed journal or unsafe sidecar")
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("sealed index is not a regular file")
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
