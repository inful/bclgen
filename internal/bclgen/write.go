package bclgen

import (
	"fmt"
	"io"
	"os"
)

// writeBinary creates the file at path and invokes fn with the open file as
// its io.Writer. fn owns the payload. All file-creation error wrapping lives
// here so individual writers (filter, locs, BCL) don't repeat the
// create/defer-close/wrap dance.
//
// On error from fn the file is left on disk in whatever partial state fn
// reached; callers higher up the stack refuse to write into a non-empty Out
// before this is reached, so a partial write here is a real failure mode
// the caller should surface as-is.
func writeBinary(path string, fn func(io.Writer) error) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if err := fn(f); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// chunkSize is the streaming chunk size used by streamChunks. 4 KiB matches
// a typical filesystem block and gzip's internal buffer, so a Write through
// a gzip.Writer hits a hot path.
const chunkSize = 4096

// maxClusterCount is the largest per-tile cluster count the on-disk formats
// can represent: the BCL header (Table 1), the filter header (Table 3) and
// the locs header (PositionsFile.cpp) all store N as a uint32.
//
// It is an int64 so that the comparison compiles on 32-bit platforms, where
// the untyped constant 0xFFFFFFFF does not fit in an int.
const maxClusterCount int64 = 0xFFFFFFFF

// streamChunks writes n bytes to w by repeatedly filling a chunkSize scratch
// buffer via fill() and writing it. Memory use is bounded regardless of n —
// the point is to write millions of cluster bytes through gzip without ever
// holding the full payload in memory.
//
// fill() must populate the slice it's given and return nil. If fill()
// returns a non-nil error the loop aborts and that error is returned
// without wrapping.
func streamChunks(w io.Writer, n int, fill func(buf []byte) error) error {
	buf := make([]byte, chunkSize)
	remaining := n
	for remaining > 0 {
		size := min(remaining, len(buf))
		if err := fill(buf[:size]); err != nil {
			return err
		}
		if _, err := w.Write(buf[:size]); err != nil {
			return fmt.Errorf("write chunk: %w", err)
		}
		remaining -= size
	}
	return nil
}
