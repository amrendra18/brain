package platform

import (
	"errors"
	"io"
	"os"
)

var ErrSourceOversize = errors.New("source exceeds the read limit")

// ReadSource opens without following links, then checks the opened file.
// A zero limit uses the opened size for owned notes, which have no source cap.
func ReadSource(path string, limit int64) ([]byte, error) {
	file, err := openSource(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0444 == 0 {
		return nil, os.ErrPermission
	}
	if limit == 0 {
		limit = info.Size()
	}
	if limit < 0 || info.Size() > limit {
		return nil, ErrSourceOversize
	}
	return readBounded(file, limit)
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit))
	if err != nil {
		return nil, err
	}
	// A separate byte avoids overflowing limit + 1 for large configured limits.
	var extra [1]byte
	n, err := io.ReadFull(reader, extra[:])
	if n != 0 {
		return nil, ErrSourceOversize
	}
	if !errors.Is(err, io.EOF) {
		return nil, err
	}
	return data, nil
}
