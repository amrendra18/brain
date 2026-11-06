package index

import (
	"encoding/json"
	"fmt"
)

// FileOutcome is the last stored result for a discovered file. Active rows
// include conversion failures and unreadable files as well as searchable files.
type FileOutcome struct {
	Source    string `json:"source"`
	Path      string `json:"path"`
	Namespace string `json:"namespace"`
	Project   string `json:"project"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
}

// ReadFileOutcomes reads active rows from the stored index without refreshing
// sources. It closes the protected reader before returning the result.
func ReadFileOutcomes(state string) (out []FileOutcome, err error) {
	stored, err := OpenStored(state)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := stored.Close(); err == nil {
			err = closeErr
		}
	}()
	rows, err := stored.Query("SELECT source,path,namespace,project,kind,status,sha256,size FROM files WHERE deleted_at IS NULL ORDER BY source COLLATE BINARY,path COLLATE BINARY")
	if err != nil {
		return nil, err
	}
	out = []FileOutcome{}
	for rows.Next() {
		var item FileOutcome
		if err = rows.Scan(&item.Source, &item.Path, &item.Namespace, &item.Project, &item.Kind, &item.Status, &item.SHA256, &item.Size); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, item)
	}
	err = rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// InventoryChunk is one searchable canonical chunk in a sealed index.
// The text is complete stored text, not the clipped public search snippet.
type InventoryChunk struct {
	ID      string      `json:"id"`
	Heading string      `json:"heading"`
	Text    string      `json:"text"`
	Project string      `json:"project"`
	Kind    string      `json:"kind"`
	Primary Reference   `json:"primary"`
	AlsoIn  []Reference `json:"also_in"`
}

// ReadChunkInventory returns every searchable canonical chunk in stable ID
// order. It uses the same active-file condition as search.Find and Totals.
func ReadChunkInventory(state string) (out []InventoryChunk, err error) {
	stored, err := OpenStored(state)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := stored.Close(); err == nil {
			err = closeErr
		}
	}()
	rows, err := stored.Query("SELECT chunks.id,chunks.heading,chunks.text,chunks.project,chunks.kind,files.source,files.path,chunks.also_in FROM chunks JOIN files ON files.id=chunks.file WHERE files.deleted_at IS NULL ORDER BY chunks.id COLLATE BINARY")
	if err != nil {
		return nil, err
	}
	out = []InventoryChunk{}
	for rows.Next() {
		var item InventoryChunk
		var aliases string
		if err = rows.Scan(&item.ID, &item.Heading, &item.Text, &item.Project, &item.Kind, &item.Primary.Source, &item.Primary.Path, &aliases); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal([]byte(aliases), &item.AlsoIn); err != nil {
			rows.Close()
			return nil, fmt.Errorf("chunk %s aliases: %w", item.ID, err)
		}
		if item.AlsoIn == nil {
			rows.Close()
			return nil, fmt.Errorf("chunk %s aliases are null", item.ID)
		}
		out = append(out, item)
	}
	err = rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}
