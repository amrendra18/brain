package settings

import (
	"brain/internal/corpus"
	"brain/internal/platform"
	"brain/internal/schedule"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Registration checks selection conflicts without creating state or settings.
func Registration(root string, scope Scope) error {
	if overlappingPaths(root, scope.State) {
		return ErrInvalid
	}
	saved, exists, err := Read(root)
	if err != nil {
		return err
	}
	if exists && saved.Resolved() != scope {
		return ErrDifferent
	}
	home, _ := os.UserHomeDir()
	defaults, err := platform.StateFolder(runtime.GOOS, home, map[string]string{"XDG_DATA_HOME": os.Getenv("XDG_DATA_HOME"), "LOCALAPPDATA": os.Getenv("LOCALAPPDATA")}, "")
	if err != nil {
		return err
	}
	if !exists && defaults != scope.State {
		if _, err := corpus.ReadDocument(filepath.Join(defaults, "config.json")); err == nil {
			return ErrDifferent
		} else if !errors.Is(err, corpus.ErrConfigMissing) {
			return err
		}
	}
	if err = CheckMutation(scope.State); err != nil {
		return err
	}
	plan, has, err := schedule.Current(scope.State)
	if err != nil {
		return err
	}
	if has && (plan.Spec.State != scope.State || plan.Spec.Config != scope.Config) {
		return ErrDifferent
	}
	return nil
}

func overlappingPaths(root, state string) bool {
	root, rootErr := resolveMissingPath(root)
	state, stateErr := resolveMissingPath(state)
	if rootErr != nil || stateErr != nil {
		return true
	}
	if contained(strings.ToLower(root), strings.ToLower(state)) || contained(strings.ToLower(state), strings.ToLower(root)) {
		return true
	}
	for left := root; ; left = filepath.Dir(left) {
		leftInfo, leftErr := os.Stat(left)
		if leftErr == nil {
			for right := state; ; right = filepath.Dir(right) {
				rightInfo, rightErr := os.Stat(right)
				if rightErr == nil && os.SameFile(leftInfo, rightInfo) {
					leftSuffix, _ := filepath.Rel(left, root)
					rightSuffix, _ := filepath.Rel(right, state)
					leftSuffix, rightSuffix = strings.ToLower(leftSuffix), strings.ToLower(rightSuffix)
					if contained(leftSuffix, rightSuffix) || contained(rightSuffix, leftSuffix) {
						return true
					}
				}
				if right == filepath.Dir(right) {
					break
				}
			}
		}
		if left == filepath.Dir(left) {
			break
		}
	}
	return false
}

func resolveMissingPath(path string) (string, error) {
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) || path == filepath.Dir(path) {
			return "", err
		}
		missing = append(missing, filepath.Base(path))
		path = filepath.Dir(path)
	}
}
