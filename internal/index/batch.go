package index

import (
	"brain/internal/chunk"
	"unsafe"
)

const workBytes = int64(unsafe.Sizeof(work{}))

// Converted pieces own their string storage, so lengths include all retained text.
func retainedBytes(item work) int64 {
	size := workBytes + int64(cap(item.Pieces))*int64(unsafe.Sizeof(chunk.Piece{}))
	entry := item.Entry
	for _, value := range []string{entry.Path, entry.Relative, entry.Source, entry.Project, entry.Kind, entry.Namespace, item.Status, item.Digest, item.Change} {
		size += int64(len(value))
	}
	if old := item.Old; old != nil {
		size += int64(unsafe.Sizeof(*old))
		for _, value := range []string{old.Path, old.Source, old.Project, old.Kind, old.Status, old.Digest, old.Namespace} {
			size += int64(len(value))
		}
	}
	for _, piece := range item.Pieces {
		size += int64(len(piece.Heading)) + int64(len(piece.Text))
	}
	return size
}
