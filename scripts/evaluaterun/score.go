package evaluaterun

import (
	"brain/internal/search"
	"fmt"
	"path/filepath"
	"sort"
)

func distinctFiles(results []search.Result, root string) []string {
	seen := map[string]bool{}
	files := []string{}
	for _, result := range results {
		paths := append([]string{}, result.AlsoIn...)
		sort.Slice(paths, func(i, j int) bool {
			return stableFileKey(paths[i], root) < stableFileKey(paths[j], root)
		})
		paths = append([]string{result.Path}, paths...)
		for _, path := range paths {
			path = filepath.Clean(path)
			if seen[path] {
				continue
			}
			seen[path] = true
			if containsPath(root, path) {
				relative, _ := filepath.Rel(root, path)
				files = append(files, filepath.ToSlash(relative))
			} else {
				files = append(files, path)
			}
		}
	}
	return files
}

func stableFileKey(name, root string) string {
	name = filepath.Clean(name)
	if containsPath(root, name) {
		relative, _ := filepath.Rel(root, name)
		return filepath.ToSlash(relative)
	}
	return filepath.ToSlash(name)
}
func score(results []search.Result, owners []string, root string) (bool, []string) {
	files := distinctFiles(results, root)
	if len(files) > 3 {
		files = files[:3]
	}
	for _, file := range files {
		for _, owner := range owners {
			if file == owner {
				return true, files
			}
		}
	}
	return false, files
}

func validateCitations(results []search.Result, known map[string]bool) error {
	for _, item := range results {
		for _, citation := range append([]string{item.Path}, item.AlsoIn...) {
			if !known[filepath.Clean(citation)] {
				return fmt.Errorf("search cited a file outside the indexed manifest")
			}
		}
	}
	return nil
}

func compactRanks(results []search.Result) []rankIdentity {
	trace := make([]rankIdentity, len(results))
	for i, item := range results {
		trace[i] = rankIdentity{Rank: i + 1, ID: item.ID, Path: item.Path, AlsoIn: append([]string{}, item.AlsoIn...), Score: item.Score}
	}
	return trace
}
