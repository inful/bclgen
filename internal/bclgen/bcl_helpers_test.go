package bclgen

import (
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
)

// Test-only BCL helpers. These live in a non-build-tagged test file so both
// the regular unit tests and the build-tagged bcl2fastq E2E test can share
// them without shipping in the production binary.

// encodeBCL writes a per-cycle BCL record to w: gzip(uint32_LE(N) || N cluster
// bytes). Convenience wrapper around writeBCLStream for tests that already
// hold the cluster bytes in a single slice.
//
// The cluster bytes must already be encoded per Table 1 (bits 0..1 = base,
// bits 2..7 = Q<<2, 0x00 = no call). See packClusterByte.
func encodeBCL(w io.Writer, n int, clusters []byte) error {
	if len(clusters) != n {
		return fmt.Errorf("cluster byte slice length %d != N %d", len(clusters), n)
	}
	return writeBCLStream(w, n, func(gz io.Writer) error {
		_, err := gz.Write(clusters)
		return err
	})
}

// decodeClusterByte is the inverse of packClusterByte. Used by tests to read a
// generated BCL back; it is unexported and so is not reachable by any
// downstream consumer.
func decodeClusterByte(b byte) (base byte, q int, noCall bool) {
	if b == 0x00 {
		return 0, 0, true
	}
	return b & 0x3, int(b >> 2), false
}

// readBCL decodes a per-cycle BCL record produced by encodeBCL.
func readBCL(r io.Reader, wantN int) ([]byte, error) {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer func() { _ = gr.Close() }()
	raw, err := io.ReadAll(gr)
	if err != nil {
		return nil, fmt.Errorf("read gz body: %w", err)
	}
	if len(raw) < bclHeaderSize {
		return nil, fmt.Errorf("BCL too short: %d bytes", len(raw))
	}
	n := int(binary.LittleEndian.Uint32(raw[:bclHeaderSize]))
	if n != wantN {
		return nil, fmt.Errorf("BCL header N=%d, want %d", n, wantN)
	}
	clusters := raw[bclHeaderSize:]
	if len(clusters) != n {
		return nil, fmt.Errorf("BCL body length %d, want %d", len(clusters), n)
	}
	out := make([]byte, n)
	copy(out, clusters)
	return out, nil
}
