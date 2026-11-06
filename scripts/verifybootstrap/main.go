package main

import (
	"crypto/sha256"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "Environment unready: verifier input path is missing.")
		os.Exit(3)
	}
	if err := check(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "Environment unready: protected verifier input cannot be certified:", err)
		os.Exit(3)
	}
}

var reviewedPackages = []string{"scripts/verifycmd", "scripts/verifyrun", "scripts/toolchain", "internal/corpus", "internal/platform", "internal/redact"}

const reviewedModSHA256 = "57138527e5a96fa7f58e37acdd417ed795f5a49ab11179ee6e23bfa790573f81"
const reviewedSumSHA256 = "3ca6de191cbcf41056b3b2c7abdcbe4b55c7ececa358c900df16bad0a700ee95"

func check(root string) error {
	data, err := checkModuleFile(root, "go.mod", reviewedModSHA256)
	if err != nil {
		return err
	}
	if _, err := checkModuleFile(root, "go.sum", reviewedSumSHA256); err != nil {
		return err
	}
	first, _, _ := strings.Cut(string(data), "\n")
	if strings.TrimSpace(first) != "module brain" {
		return fmt.Errorf("module identity differs from the reviewed project")
	}
	for _, directory := range reviewedPackages {
		if err := checkDirectory(root, directory); err != nil {
			return err
		}
	}
	return nil
}

func checkModuleFile(root, name, expected string) ([]byte, error) {
	path := filepath.Join(root, name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("module input is unavailable or unsafe")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("module input cannot be read")
	}
	digest := sha256.Sum256(data)
	if fmt.Sprintf("%x", digest) != expected {
		return nil, fmt.Errorf("module input differs from the reviewed dependency")
	}
	return data, nil
}

func checkDirectory(root, directory string) error {
	return filepath.WalkDir(filepath.Join(root, directory), func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		if relative == directory {
			if !entry.IsDir() {
				return fmt.Errorf("%s is not a directory", relative)
			}
			return nil
		}
		if excluded(relative) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			if buildable(filepath.Base(relative)) {
				return fmt.Errorf("%s", relative)
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || (!entry.IsDir() && !entry.Type().IsRegular()) {
			return fmt.Errorf("%s is not a regular source", relative)
		}
		if entry.IsDir() {
			return nil
		}
		if strings.HasSuffix(relative, "_test.go") {
			return nil
		}
		if !strings.HasSuffix(relative, ".go") {
			// The verifier uses only Go source. C, assembly, and generated object
			// inputs need a new review before this command can compile them.
			if buildable(filepath.Base(relative)) || cgoInput(relative) {
				return fmt.Errorf("%s is an unclassified compiler input", relative)
			}
			return nil
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		return checkGo(relative, data)
	})
}

func checkGo(relative string, data []byte) error {
	parsed, err := parser.ParseFile(token.NewFileSet(), relative, data, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("%s imports cannot be classified: %w", relative, err)
	}
	for _, imported := range parsed.Imports {
		value, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			return fmt.Errorf("%s import cannot be classified", relative)
		}
		if excluded(value) {
			return fmt.Errorf("%s imports protected input", relative)
		}
		if value == "C" || (strings.Contains(strings.Split(value, "/")[0], ".") && value != "modernc.org/sqlite") || (strings.HasPrefix(value, "brain/") && !slices.Contains(reviewedPackages, strings.TrimPrefix(value, "brain/"))) {
			return fmt.Errorf("%s imports unreviewed input", relative)
		}
	}
	for _, group := range parsed.Comments {
		for _, comment := range group.List {
			if strings.HasPrefix(comment.Text, "//go:embed") || strings.HasPrefix(comment.Text, "// #cgo") || strings.HasPrefix(comment.Text, "//go:cgo") {
				return fmt.Errorf("%s has an unclassified compiler directive", relative)
			}
		}
	}
	return nil
}

func buildable(name string) bool {
	return (strings.HasSuffix(strings.ToLower(name), ".go") || strings.HasSuffix(strings.ToLower(name), ".s") || strings.HasSuffix(strings.ToLower(name), ".syso")) && !strings.HasPrefix(name, ".") && !strings.HasPrefix(name, "_")
}

func cgoInput(name string) bool {
	for _, suffix := range []string{".c", ".cc", ".cpp", ".h", ".m", ".f", ".f90"} {
		if strings.HasSuffix(strings.ToLower(name), suffix) {
			return true
		}
	}
	return false
}

func excluded(name string) bool {
	parts := strings.Split(filepath.ToSlash(name), "/")
	for i, part := range parts {
		lower := strings.ToLower(part)
		if lower == ".claude" && i+1 < len(parts) && strings.EqualFold(parts[i+1], "worktrees") {
			return true
		}
		switch lower {
		case ".git", ".local", "node_modules", "dist", ".venv", ".stryker-tmp", "auth.json":
			return true
		}
		if strings.HasPrefix(lower, ".env") || strings.HasSuffix(lower, ".pem") || strings.HasSuffix(lower, ".key") || strings.HasSuffix(lower, ".jsonl") || reserved(part) {
			return true
		}
	}
	return false
}

func reserved(name string) bool {
	if end := strings.IndexAny(name, ".:"); end >= 0 {
		name = name[:end]
	}
	name = strings.ToUpper(strings.TrimRight(name, " "))
	switch name {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if strings.HasPrefix(name, "COM") || strings.HasPrefix(name, "LPT") {
		switch name[3:] {
		case "1", "2", "3", "4", "5", "6", "7", "8", "9", "¹", "²", "³":
			return true
		}
	}
	return false
}
