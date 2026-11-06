package settings

import (
	"brain/internal/platform"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var ErrScope = errors.New("invalid selected scope")
var ErrInvalid = errors.New("saved settings are invalid or unsafe")
var ErrDifferent = errors.New("an existing selection requires relocation")
var ErrPending = errors.New("state relocation requires explicit recovery")

type Scope struct {
	State  string `json:"state_dir"`
	Config string `json:"config"`
}
type Selection struct {
	Version int `json:"version"`
	Scope
}

func Root() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return "", ErrInvalid
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Preferences", "Brain"), nil
	case "windows":
		if app := os.Getenv("APPDATA"); filepath.IsAbs(app) {
			return filepath.Join(app, "Brain"), nil
		}
		return filepath.Join(home, "AppData", "Roaming", "Brain"), nil
	default:
		if xdg := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(xdg) {
			return filepath.Join(xdg, "brain"), nil
		}
		return filepath.Join(home, ".config", "brain"), nil
	}
}
func validPath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.ContainsRune(path, 0) && !platform.NetworkPath(runtime.GOOS, path)
}
func (s Selection) valid() bool {
	return s.Version == 1 && validPath(s.State) && (s.Config == "" || validPath(s.Config))
}
func decode(data []byte, target any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(target) != nil {
		return ErrInvalid
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return ErrInvalid
	}
	return nil
}
func Read(root string) (Selection, bool, error) {
	if err := platform.CheckPrivateState(root); errors.Is(err, os.ErrNotExist) {
		return Selection{}, false, nil
	} else if err != nil {
		return Selection{}, false, ErrInvalid
	}
	data, err := platform.ReadPrivateFile(root, "selection.json", 65536)
	if errors.Is(err, os.ErrNotExist) {
		return Selection{}, false, nil
	}
	var s Selection
	if err != nil || decode(data, &s) != nil || !s.valid() {
		return s, false, ErrInvalid
	}
	return s, true, nil
}
func Save(root string, s Selection) error {
	if !s.valid() {
		return ErrInvalid
	}
	if err := platform.PrepareState(root); err != nil {
		return err
	}
	data, _ := json.Marshal(s)
	return platform.WriteState(root, "selection.json", append(data, '\n'))
}
func (s Selection) Resolved() Scope {
	scope := s.Scope
	if scope.Config == "" {
		scope.Config = filepath.Join(scope.State, "config.json")
	}
	return scope
}
func Resolve(root, stateFlag, configFlag string, configPresent bool) (Scope, bool, error) {
	state := stateFlag
	if state == "" {
		state = os.Getenv("BRAIN_HOME")
	}
	var saved Selection
	var exists bool
	var err error
	if state == "" {
		saved, exists, err = Read(root)
		if err != nil {
			return Scope{}, false, err
		}
		if exists {
			state = saved.State
		}
	}
	state, err = platform.CurrentState(state)
	if err != nil {
		return Scope{}, false, errors.Join(ErrScope, err)
	}
	config := configFlag
	if !configPresent {
		config, configPresent = os.LookupEnv("BRAIN_CONFIG")
	}
	explicit := configPresent
	if !configPresent && exists && saved.State == state && saved.Config != "" {
		config = saved.Config
		explicit = true
		configPresent = true
	}
	if !configPresent {
		config = filepath.Join(state, "config.json")
	}
	if strings.TrimSpace(config) == "" || strings.ContainsRune(config, 0) {
		return Scope{}, false, ErrScope
	}
	config, err = filepath.Abs(config)
	return Scope{state, config}, explicit, err
}
func Selected(scope Scope) Selection {
	s := Selection{Version: 1, Scope: scope}
	if s.Config == filepath.Join(s.State, "config.json") {
		s.Config = ""
	}
	return s
}
func CheckMutation(state string) error {
	_, err := platform.ReadPrivateFile(state, "relocation.json", 65536)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return ErrPending
}

func CheckRead(state string) error {
	data, err := platform.ReadPrivateFile(state, "relocation.json", 65536)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	var marker stateMarker
	if err != nil {
		return err
	}
	if decode(data, &marker) != nil || marker.ID == "" || marker.Destination {
		return ErrPending
	}
	return nil
}
