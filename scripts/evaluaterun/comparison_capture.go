package evaluaterun

import (
	"brain/internal/corpus"
	"brain/internal/index"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
)

// comparisonCapture is written before any comparison query runs.
type comparisonCapture struct {
	Version            int                 `json:"version"`
	State              string              `json:"state"`
	CapturedBinary     string              `json:"captured_binary"`
	BinarySHA256       string              `json:"binary_sha256"`
	EffectiveConfigSHA string              `json:"effective_config_sha256"`
	ManifestSHA256     string              `json:"manifest_sha256"`
	Files              []manifestRow       `json:"files"`
	Outcomes           []index.FileOutcome `json:"outcomes"`
	OutcomesSHA256     string              `json:"outcomes_sha256"`
	InventorySHA256    string              `json:"inventory_sha256"`
	DatabaseSHA256     string              `json:"database_sha256"`
	IndexChunks        int                 `json:"index_chunks"`
	IndexGaps          []corpus.Skipped    `json:"index_gaps"`
	IndexCommand       commandRow          `json:"index_command"`
}

func captureComparisonIndex(ctx context.Context, repo, root, state, binary string, config corpus.Config, rawConfig map[string]json.RawMessage, before []manifestRow, skipped []corpus.Skipped, issue func(*driver, context.Context, ...string) commandRow) (comparisonCapture, []index.InventoryChunk, error) {
	var capture comparisonCapture
	if err := os.Mkdir(state, 0700); err != nil {
		return capture, nil, err
	}
	var rawSources []map[string]json.RawMessage
	if err := json.Unmarshal(rawConfig["sources"], &rawSources); err != nil || len(rawSources) != len(config.Sources) {
		return capture, nil, errors.New("comparison source objects are invalid")
	}
	for i, source := range config.Sources {
		if rawSources[i] == nil {
			return capture, nil, errors.New("comparison source object is invalid")
		}
		encoded, err := json.Marshal(source.Path)
		if err != nil {
			return capture, nil, err
		}
		rawSources[i]["path"] = encoded
		delete(rawSources[i], "kind")
	}
	configCopy := make(map[string]json.RawMessage, len(rawConfig))
	for key, value := range rawConfig {
		configCopy[key] = append([]byte{}, value...)
	}
	encodedSources, err := json.Marshal(rawSources)
	if err != nil {
		return capture, nil, err
	}
	configCopy["sources"] = encodedSources
	effective, err := json.MarshalIndent(configCopy, "", "  ")
	if err != nil {
		return capture, nil, err
	}
	effective = append(effective, '\n')
	if err := os.WriteFile(filepath.Join(state, "config.json"), effective, 0600); err != nil {
		return capture, nil, err
	}
	name := filepath.Base(state)
	scratch := filepath.Join(root, "scratch-"+name)
	home := filepath.Join(root, "home-"+name)
	for _, dir := range []string{scratch, home} {
		if err := os.Mkdir(dir, 0700); err != nil {
			return capture, nil, err
		}
	}
	captured, binarySHA, err := captureBinary(binary, repo, scratch)
	if err != nil {
		return capture, nil, err
	}
	d := &driver{binary: captured, state: state, home: home}
	indexCommand := issue(d, ctx, "index")
	reported := 0
	gaps, err := indexReply(indexCommand, &reported)
	if err != nil {
		return capture, nil, err
	}
	outcomes, err := index.ReadFileOutcomes(state)
	if err != nil {
		return capture, nil, err
	}
	byFile := make(map[string]manifestRow, len(before))
	for _, file := range before {
		byFile[filepath.Join(file.Source, filepath.FromSlash(file.Path))] = file
	}
	seen := make(map[string]bool, len(outcomes))
	for _, item := range outcomes {
		identity := filepath.Join(item.Source, filepath.FromSlash(item.Path))
		file, found := byFile[identity]
		if !found || seen[identity] || file.Status == "eligible" && item.SHA256 != file.SHA256 {
			return capture, nil, errors.New("indexed file differs from frozen manifest")
		}
		seen[identity] = true
	}
	for identity, file := range byFile {
		if file.Status == "eligible" && !seen[identity] {
			return capture, nil, errors.New("eligible file is missing from index")
		}
	}
	indexGaps := make([]corpus.Skipped, len(gaps))
	for i, gap := range gaps {
		indexGaps[i] = corpus.Skipped{Path: gap.Path, Reason: gap.Reason}
	}
	inventory, err := index.ReadChunkInventory(state)
	if err != nil {
		return capture, nil, err
	}
	if len(inventory) > 32765 {
		return capture, nil, errors.New("adapter-capacity-exceeded")
	}
	if reported != len(inventory) {
		return capture, nil, errors.New("index reply chunk count differs from inventory")
	}
	after, afterSkipped, _, err := scanManifest(config)
	if err != nil {
		return capture, nil, err
	}
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(skipped, afterSkipped) {
		return capture, nil, errors.New("corpus changed during capture")
	}
	manifestBytes, err := json.Marshal(before)
	if err != nil {
		return capture, nil, err
	}
	outcomeBytes, err := json.Marshal(outcomes)
	if err != nil {
		return capture, nil, err
	}
	inventoryBytes, err := json.Marshal(inventory)
	if err != nil {
		return capture, nil, err
	}
	databaseSHA, err := sealedDatabaseSHA(state, repo)
	if err != nil {
		return capture, nil, err
	}
	capture = comparisonCapture{Version: 1, State: state, CapturedBinary: captured, BinarySHA256: binarySHA, EffectiveConfigSHA: digest(effective), ManifestSHA256: digest(manifestBytes), Files: before, Outcomes: outcomes, OutcomesSHA256: digest(outcomeBytes), InventorySHA256: digest(inventoryBytes), DatabaseSHA256: databaseSHA, IndexChunks: len(inventory), IndexGaps: indexGaps, IndexCommand: indexCommand}
	return capture, inventory, nil
}

func validateComparisonCapture(c comparisonCapture, repo string) ([]index.InventoryChunk, error) {
	if c.Version != 1 || c.State == "" || c.CapturedBinary == "" || c.IndexChunks < 0 || c.IndexChunks > 32765 {
		return nil, errors.New("capture identity is invalid")
	}
	if c.IndexCommand.Exit != 0 && c.IndexCommand.Exit != 1 && c.IndexCommand.Exit != 3 {
		return nil, errors.New("capture index command failed")
	}
	if len(c.IndexCommand.Argv) != 6 || c.IndexCommand.Argv[0] != c.CapturedBinary || c.IndexCommand.Argv[1] != "index" || c.IndexCommand.Argv[3] != c.State {
		return nil, errors.New("capture index command differs")
	}
	count := 0
	if _, err := indexReply(c.IndexCommand, &count); err != nil || count != c.IndexChunks {
		return nil, errors.New("capture index reply differs")
	}
	manifestBytes, err := json.Marshal(c.Files)
	if err != nil || digest(manifestBytes) != c.ManifestSHA256 {
		return nil, errors.New("capture manifest differs")
	}
	outcomesBytes, err := json.Marshal(c.Outcomes)
	if err != nil || digest(outcomesBytes) != c.OutcomesSHA256 {
		return nil, errors.New("capture outcomes differ")
	}
	binaryBytes, _, err := readControl(c.CapturedBinary, repo, maxCapturedBinaryBytes)
	if err != nil || digest(binaryBytes) != c.BinarySHA256 {
		return nil, errors.New("capture binary differs")
	}
	databaseSHA, err := sealedDatabaseSHA(c.State, repo)
	if err != nil || databaseSHA != c.DatabaseSHA256 {
		return nil, errors.New("resume-corpus-invalid")
	}
	inventory, err := index.ReadChunkInventory(c.State)
	if err != nil || len(inventory) != c.IndexChunks {
		return nil, errors.New("resume-corpus-invalid")
	}
	inventoryBytes, err := json.Marshal(inventory)
	if err != nil || digest(inventoryBytes) != c.InventorySHA256 {
		return nil, errors.New("resume-corpus-invalid")
	}
	actualOutcomes, err := index.ReadFileOutcomes(c.State)
	if err != nil || !reflect.DeepEqual(actualOutcomes, c.Outcomes) {
		return nil, fmt.Errorf("resume-corpus-invalid: file outcomes differ")
	}
	return inventory, nil
}
