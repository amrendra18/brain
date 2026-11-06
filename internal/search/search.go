package search

import (
	"brain/internal/index"
	"brain/internal/query"
	"database/sql"
	"encoding/json"
	"math"
	"strings"
	"unicode/utf8"
)

type Result struct {
	ID        string   `json:"id"`
	Path      string   `json:"path"`
	Project   string   `json:"project"`
	Kind      string   `json:"kind"`
	Heading   string   `json:"heading"`
	Score     float64  `json:"score"`
	Snippet   string   `json:"snippet"`
	Bytes     int      `json:"bytes"`
	Truncated bool     `json:"truncated"`
	AlsoIn    []string `json:"also_in"`
}
type Query struct {
	AsRun string `json:"as_run"`
	Mode  string `json:"mode"`
}
type Reply struct {
	Count   int      `json:"count"`
	Total   int      `json:"total"`
	Empty   bool     `json:"empty"`
	Query   Query    `json:"query"`
	Results []Result `json:"results"`
}
type Chunk struct {
	ID        string  `json:"id"`
	Path      string  `json:"path"`
	Project   string  `json:"project"`
	Kind      string  `json:"kind"`
	Heading   string  `json:"heading"`
	Text      string  `json:"text"`
	Bytes     int     `json:"bytes"`
	Truncated bool    `json:"truncated"`
	Prev      *string `json:"prev"`
	NextChunk *string `json:"next_chunk"`
}

func Clip(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	text = text[:limit]
	for !utf8.ValidString(text) && len(text) > 0 {
		text = text[:len(text)-1]
	}
	return text
}
func Projects(db *sql.DB) ([]string, error) {
	rows, err := db.Query("SELECT DISTINCT project FROM files WHERE deleted_at IS NULL AND status IN ('current','superseded') ORDER BY project")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return nil, err
		}
		result = append(result, name)
	}
	return result, rows.Err()
}
func Find(db *sql.DB, plan *query.Plan, project, kind string, limit int, full bool) (Reply, error) {
	reply := Reply{Query: Query{plan.First, plan.Mode}, Results: []Result{}}
	tx, err := db.Begin()
	if err != nil {
		return reply, err
	}
	defer tx.Rollback()
	results, total, err := rank(tx, plan.First, plan.Boost, project, kind, limit, full)
	if err == nil && total == 0 && plan.Fallback != "" {
		reply.Query = Query{plan.Fallback, "or"}
		results, total, err = rank(tx, plan.Fallback, plan.Boost, project, kind, limit, full)
	}
	if err != nil {
		return reply, err
	}
	reply.Results, reply.Total, reply.Count = results, total, len(results)
	reply.Empty = len(results) == 0
	return reply, tx.Commit()
}
func rank(tx *sql.Tx, expression string, boost []string, project, kind string, limit int, full bool) ([]Result, int, error) {
	where := "chunk_fts MATCH ? AND files.deleted_at IS NULL"
	parameters := []any{expression}
	if project != "" {
		where += " AND chunks.project=?"
		parameters = append(parameters, project)
	}
	if kind != "" {
		where += " AND chunks.kind=?"
		parameters = append(parameters, kind)
	}
	joins := " FROM chunk_fts JOIN chunks ON chunks.row=chunk_fts.rowid JOIN files ON files.id=chunks.file"
	total := 0
	if err := tx.QueryRow("SELECT count(*)"+joins+" WHERE "+where, parameters...).Scan(&total); err != nil {
		return nil, 0, err
	}
	result := []Result{}
	if total == 0 {
		return result, 0, nil
	}
	factor := "(CASE WHEN files.status='superseded' THEN 0.5 ELSE 1.0 END)*(CASE WHEN chunks.kind='third-party' THEN 0.5 ELSE 1.0 END)"
	args := []any{}
	if len(boost) > 0 {
		factor += "*(CASE WHEN chunks.project IN (" + strings.TrimSuffix(strings.Repeat("?,", len(boost)), ",") + ") THEN 1.5 ELSE 1.0 END)"
		for _, name := range boost {
			args = append(args, name)
		}
	}
	args = append(args, parameters...)
	args = append(args, limit)
	rows, err := tx.Query("SELECT chunks.row,bm25(chunk_fts,3.0,1.0,0.5,0.5)*"+factor+" AS score"+joins+" WHERE "+where+" ORDER BY score,chunks.row LIMIT ?", args...)
	if err != nil {
		return nil, 0, err
	}
	ordered, scores := []int64{}, map[int64]float64{}
	for rows.Next() {
		var row int64
		var score float64
		if err = rows.Scan(&row, &score); err != nil {
			rows.Close()
			return nil, 0, err
		}
		ordered = append(ordered, row)
		scores[row] = score
	}
	problem := rows.Err()
	rows.Close()
	if problem != nil {
		return nil, 0, problem
	}
	if len(ordered) == 0 {
		return result, total, nil
	}
	args = []any{expression}
	for _, row := range ordered {
		args = append(args, row)
	}
	rows, err = tx.Query("SELECT chunks.row,chunks.id,files.source,files.path,chunks.project,chunks.kind,chunks.heading,chunks.text,chunks.bytes,chunks.also_in,snippet(chunk_fts,1,'','',' ... ',64)"+joins+" WHERE chunk_fts MATCH ? AND chunks.row IN ("+strings.TrimSuffix(strings.Repeat("?,", len(ordered)), ",")+")", args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	details := map[int64]Result{}
	for rows.Next() {
		var row int64
		var source, path, text, also, snippet string
		var item Result
		if err = rows.Scan(&row, &item.ID, &source, &path, &item.Project, &item.Kind, &item.Heading, &text, &item.Bytes, &also, &snippet); err != nil {
			return nil, 0, err
		}
		item.Path = index.Reference{Source: source, Path: path}.Absolute()
		item.Score = math.Round(-scores[row]*100) / 100
		item.Truncated = !full && item.Bytes > 600
		item.Snippet = text
		if item.Truncated {
			item.Snippet = Clip(snippet, 600)
		}
		var references []index.Reference
		if err = json.Unmarshal([]byte(also), &references); err != nil {
			return nil, 0, err
		}
		item.AlsoIn = []string{}
		for _, reference := range references {
			item.AlsoIn = append(item.AlsoIn, reference.Absolute())
		}
		details[row] = item
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	for _, row := range ordered {
		result = append(result, details[row])
	}
	return result, total, nil
}
func Show(db *sql.DB, id string, full bool) (*Chunk, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	item := &Chunk{ID: id}
	var file int64
	var source, path string
	err = tx.QueryRow("SELECT chunks.file,files.source,files.path,chunks.project,chunks.kind,chunks.heading,chunks.text,chunks.bytes FROM chunks JOIN files ON files.id=chunks.file WHERE chunks.id=? AND files.deleted_at IS NULL", id).Scan(&file, &source, &path, &item.Project, &item.Kind, &item.Heading, &item.Text, &item.Bytes)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	item.Path = index.Reference{Source: source, Path: path}.Absolute()
	item.Truncated = !full && item.Bytes > 1500
	if item.Truncated {
		item.Text = Clip(item.Text, 1500)
	}
	var ordinal int
	if err = tx.QueryRow("SELECT min(ordinal) FROM places WHERE file=? AND chunk=?", file, id).Scan(&ordinal); err != nil {
		return nil, err
	}
	for _, neighbour := range []struct {
		comparison, order string
		target            **string
	}{{"<", "DESC", &item.Prev}, {">", "ASC", &item.NextChunk}} {
		var found string
		// A repeated chunk has one public ID, so navigate by each ID's first position.
		err = tx.QueryRow("SELECT chunk FROM places WHERE file=? GROUP BY chunk HAVING min(ordinal) "+neighbour.comparison+"? ORDER BY min(ordinal) "+neighbour.order+" LIMIT 1", file, ordinal).Scan(&found)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		if err == nil {
			value := found
			*neighbour.target = &value
		}
	}
	return item, tx.Commit()
}
func LastHeading(db *sql.DB, id string) string {
	var heading string
	db.QueryRow("SELECT heading FROM chunks WHERE id=? UNION ALL SELECT heading FROM retired WHERE id=? LIMIT 1", id, id).Scan(&heading)
	return heading
}
