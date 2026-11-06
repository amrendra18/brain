package evaluaterun

import (
	"brain/internal/corpus"
	"brain/internal/index"
)

const scoringVersion = "first-three-distinct-files-v1"

type questionSet struct {
	SchemaVersion int        `json:"schema_version"`
	Status        string     `json:"status"`
	ScoringRule   string     `json:"scoring_rule"`
	ExactNameRule string     `json:"exact_name_rule"`
	Entries       []question `json:"entries"`
}
type question struct {
	ID       string   `json:"id"`
	Question string   `json:"question"`
	Query    string   `json:"query"`
	Keywords []string `json:"keywords"`
	Owners   []string `json:"owners"`
	Evidence []struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"evidence"`
	ExactName       *bool  `json:"exact_name"`
	ExactNameAnchor string `json:"exact_name_anchor"`
	ExactNameReason string `json:"exact_name_reason"`
}
type manifestRow struct {
	Source, Path, Namespace, Project, Kind string
	Size                                   int64
	MtimeNS                                int64
	SHA256                                 string
	Status                                 string
}
type commandRow struct {
	Argv       []string `json:"argv"`
	Exit       int      `json:"exit"`
	DurationMS int64    `json:"duration_ms"`
	Stdout     string   `json:"stdout"`
	Stderr     string   `json:"stderr"`
}
type resultRow struct {
	ID         string         `json:"id"`
	Query      string         `json:"query"`
	QueryAsRun string         `json:"query_as_run"`
	QueryMode  string         `json:"query_mode"`
	Total      int            `json:"total"`
	Status     string         `json:"status"`
	Hit        *bool          `json:"hit"`
	TopThree   []string       `json:"top_three"`
	Ranked     []rankIdentity `json:"ranked"`
	DurationMS int64          `json:"duration_ms"`
	Error      string         `json:"error,omitempty"`
}

type rankIdentity struct {
	Rank   int      `json:"rank"`
	ID     string   `json:"id"`
	Path   string   `json:"path"`
	AlsoIn []string `json:"also_in"`
	Score  float64  `json:"score"`
}
type resultRecord struct {
	Version                 string              `json:"version"`
	Revision                string              `json:"revision"`
	Status                  string              `json:"status"`
	CorpusRoot              string              `json:"corpus_root"`
	KeywordSHA256           string              `json:"keyword_sha256"`
	KeywordSnapshot         []byte              `json:"keyword_snapshot"`
	SentenceSHA256          string              `json:"sentence_sha256"`
	SentenceSnapshot        []byte              `json:"sentence_snapshot"`
	ConfigSHA256            string              `json:"config_sha256"`
	ConfigSnapshot          []byte              `json:"config_snapshot"`
	EffectiveConfigSHA256   string              `json:"effective_config_sha256"`
	EffectiveConfigSnapshot []byte              `json:"effective_config_snapshot"`
	AdapterSHA256           string              `json:"adapter_sha256"`
	BinarySHA256            string              `json:"binary_sha256"`
	CapturedBinary          string              `json:"captured_binary"`
	BinaryOriginal          string              `json:"binary_original"`
	ManifestSHA256          string              `json:"manifest_sha256"`
	IndexChunks             int                 `json:"index_chunks"`
	Sources                 []corpus.Source     `json:"sources"`
	EffectiveExclude        []string            `json:"effective_exclude"`
	HitCount                int                 `json:"hit_count"`
	Rows                    []resultRow         `json:"rows"`
	Files                   []manifestRow       `json:"files"`
	Outcomes                []index.FileOutcome `json:"outcomes"`
	Skipped                 []corpus.Skipped    `json:"skipped"`
	Commands                []commandRow        `json:"commands"`
}
type floorRecord struct {
	Version           string `json:"version"`
	KeywordSHA256     string `json:"keyword_sha256"`
	SentenceSHA256    string `json:"sentence_sha256"`
	ScoringVersion    string `json:"scoring_version"`
	FirstResultSHA256 string `json:"first_result_sha256"`
	HitCount          int    `json:"hit_count"`
}
