package redact

import (
	"brain/internal/platform"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"
)

const FileLimit = 1 << 20

func Load(path string) (*Policy, error) {
	if path == "" {
		return Default(), nil
	}
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) || platform.NetworkPath(runtime.GOOS, path) {
		return nil, ErrPolicyInvalid
	}
	path = filepath.Clean(path)
	if runtime.GOOS == "darwin" {
		for _, alias := range []string{"/tmp", "/var", "/etc"} {
			if strings.HasPrefix(path, alias+"/") {
				path = "/private" + path
				break
			}
		}
	}
	if remote, known := platform.RemoteFolder(filepath.Dir(path)); remote || !known {
		return nil, ErrPolicyInvalid
	}
	volume := filepath.VolumeName(path) + string(filepath.Separator)
	data, err := platform.ReadPrivateFile(volume, strings.TrimPrefix(path, volume), FileLimit)
	if err != nil || !utf8.Valid(data) {
		return nil, ErrPolicyInvalid
	}
	var values []string
	if json.Unmarshal(data, &values) != nil || values == nil {
		return nil, ErrPolicyInvalid
	}
	if !pairedSurrogates(data) {
		return nil, ErrPolicyInvalid
	}
	return newPolicy(values)
}

func pairedSurrogates(data []byte) bool {
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		i++
		if data[i] != 'u' {
			continue
		}
		code, _ := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		i += 4
		if code >= 0xdc00 && code <= 0xdfff {
			return false
		}
		if code < 0xd800 || code > 0xdbff {
			continue
		}
		if i+6 >= len(data) || string(data[i+1:i+3]) != "\\u" {
			return false
		}
		low, _ := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
		if low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	return true
}
