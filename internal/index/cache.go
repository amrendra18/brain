package index

import (
	"brain/internal/chunk"
	"brain/internal/convert"
	"brain/internal/platform"
	"brain/internal/redact"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func sourceText(state, path, digest string, data []byte, policies ...*redact.Policy) (string, error) {
	policy := redact.Default()
	if len(policies) > 0 {
		policy = policies[0]
	}
	if !convert.IsConverted(path) {
		return policy.Apply(strings.ToValidUTF8(string(data), "�")).Text, nil
	}
	name := filepath.Join("converted", cacheName(path, digest, policy))
	if cached, err := platform.ReadState(state, name); err == nil {
		return policy.Apply(strings.ToValidUTF8(string(cached), "�")).Text, nil
	}
	var text string
	var err error
	if strings.EqualFold(filepath.Ext(path), ".docx") {
		text, err = convert.Docx(data)
	} else {
		text, err = convert.Rtf(data)
	}
	if err != nil {
		return "", err
	}
	text = policy.Apply(text).Text
	// A failed cache write leaves useful text. The next full index tries again.
	root, err := os.OpenRoot(state)
	if err == nil {
		defer root.Close()
		if platform.SafeStatePath(state, name) == nil && root.MkdirAll("converted", 0700) == nil {
			_ = platform.WriteState(state, name, []byte(text))
		}
	}
	return text, nil
}

func pruneCache(db *sql.DB, state string, policies ...*redact.Policy) error {
	policy := redact.Default()
	if len(policies) > 0 {
		policy = policies[0]
	}
	if err := platform.SafeStatePath(state, "converted"); err != nil {
		return err
	}
	root, err := os.OpenRoot(state)
	if err != nil {
		return err
	}
	defer root.Close()
	folder, err := root.Open("converted")
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer folder.Close()
	names, err := folder.Readdirnames(-1)
	if err != nil {
		return err
	}
	rows, err := db.Query("SELECT path,sha256 FROM files WHERE chunker_version=?", chunk.Version)
	if err != nil {
		return err
	}
	kept := map[string]bool{}
	for rows.Next() {
		var path, digest string
		if err = rows.Scan(&path, &digest); err != nil {
			rows.Close()
			return err
		}
		if convert.IsConverted(path) {
			kept[cacheName(path, digest, policy)] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, name := range names {
		if !kept[name] {
			if err = root.Remove(filepath.Join("converted", name)); err != nil {
				return err
			}
		}
	}
	return nil
}

func cacheName(path, digest string, policies ...*redact.Policy) string {
	policy := redact.Default()
	if len(policies) > 0 {
		policy = policies[0]
	}
	return policy.Fingerprint() + "-v" + strconv.Itoa(chunk.Version) + "-" + strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".") + "-" + digest + ".txt"
}
