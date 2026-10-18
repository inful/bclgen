package bclgen

import (
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
)

// readBCL decodes a per-cycle BCL record produced by encodeBCL. Lives in
// a non-build-tagged test file so both the regular unit tests and the
// build-tagged bcl2fastq E2E test can share it.
func readBCL(r io.Reader, wantN int) ([]byte, error) {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("bclgen: gzip reader: %w", err)
	}
	defer func() { _ = gr.Close() }()
	raw, err := io.ReadAll(gr)
	if err != nil {
		return nil, fmt.Errorf("bclgen: read gz body: %w", err)
	}
	if len(raw) < bclHeaderSize {
		return nil, fmt.Errorf("bclgen: BCL too short: %d bytes", len(raw))
	}
	n := int(binary.LittleEndian.Uint32(raw[:bclHeaderSize]))
	if n != wantN {
		return nil, fmt.Errorf("bclgen: BCL header N=%d, want %d", n, wantN)
	}
	clusters := raw[bclHeaderSize:]
	if len(clusters) != n {
		return nil, fmt.Errorf("bclgen: BCL body length %d, want %d", len(clusters), n)
	}
	out := make([]byte, n)
	copy(out, clusters)
	return out, nil
}