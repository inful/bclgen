package bclgen

import (
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
)

// bclHeaderSize is the byte length of the per-cycle BCL header (Table 1,
// "Bytes 0–3 Number of N cluster Unsigned 32 bits integer").
const bclHeaderSize = 4

// Bases used in cluster bytes. Order matches Table 1 in the bcl2fastq guide.
const (
	BaseA byte = 0
	BaseC byte = 1
	BaseG byte = 2
	BaseT byte = 3
)

// packClusterByte encodes one cluster byte per Table 1.
//
//	bits 0..1 = base (A=0, C=1, G=2, T=3)
//	bits 2..7 = Q-score shifted up by 2
//
// If the byte is 0x00 the cluster is "no call". NoCall takes precedence.
func packClusterByte(base byte, q int) byte {
	if q < 0 {
		q = 0
	}
	if q > 63 {
		q = 63
	}
	return byte(q<<2) | (base & 0x3)
}

// decodeClusterByte is the inverse of packClusterByte. Used by tests and any
// downstream consumer that wants to read a generated BCL back.
func decodeClusterByte(b byte) (base byte, q int, noCall bool) {
	if b == 0x00 {
		return 0, 0, true
	}
	return b & 0x3, int(b >> 2), false
}

// writeBCLStream writes the per-cycle BCL record to w without ever holding
// more than ~4 KiB plus gzip's internal buffer in memory, regardless of n.
// It writes the 4-byte little-endian N header, calls produce() to emit the
// cluster bytes into an internal gzip writer, and closes the gzip footer.
//
// produce() must write exactly N bytes to the supplied io.Writer. If
// produce() writes fewer or returns a non-nil error mid-stream, the gzip
// footer is closed and the produce error (wrapped) is returned.
//
// Use this whenever n is large (≥ 1e6 clusters), so a fully-allocated
// cluster slice would dominate memory. The produced bytes are identical to
// the byte-level equivalent of one big gzip.Write — gzip's output is
// deterministic given input bytes.
func writeBCLStream(w io.Writer, n int, produce func(io.Writer) error) error {
	if n > 0xFFFFFFFF {
		return fmt.Errorf("bclgen: N=%d exceeds 32-bit header", n)
	}
	var hdr [bclHeaderSize]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(n))

	gz := gzip.NewWriter(w)
	if _, err := gz.Write(hdr[:]); err != nil {
		return fmt.Errorf("bclgen: gzip write header: %w", err)
	}
	if err := produce(gz); err != nil {
		// Discard close error: produce's error is the more useful one.
		_ = gz.Close()
		return fmt.Errorf("bclgen: produce clusters: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("bclgen: gzip close: %w", err)
	}
	return nil
}

// encodeBCL writes a per-cycle BCL record to w: gzip(uint32_LE(N) || N cluster bytes).
//
// Convenience wrapper around writeBCLStream for callers (typically tests)
// that already have the cluster bytes in a single slice.
//
// The cluster bytes must already be encoded per Table 1 (bits 0..1 = base,
// bits 2..7 = Q<<2, 0x00 = no call). See packClusterByte.
func encodeBCL(w io.Writer, n int, clusters []byte) error {
	if len(clusters) != n {
		return fmt.Errorf("bclgen: cluster byte slice length %d != N %d", len(clusters), n)
	}
	return writeBCLStream(w, n, func(gz io.Writer) error {
		_, err := gz.Write(clusters)
		return err
	})
}

// readBCL decodes a per-cycle BCL record produced by encodeBCL. Used in tests.
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

// sequenceBase returns the base (A=0, C=1, G=2, T=3) for the given
// 0-based cycle index, or (0, true) for a no-call ('N'). Returns an error
// if the sequence is empty or contains an invalid character.
func sequenceBase(seq string, cycle int) (base byte, noCall bool, err error) {
	if seq == "" {
		return 0, false, fmt.Errorf("bclgen: empty sequence")
	}
	ch := seq[cycle%len(seq)]
	switch ch {
	case 'A', 'a':
		return BaseA, false, nil
	case 'C', 'c':
		return BaseC, false, nil
	case 'G', 'g':
		return BaseG, false, nil
	case 'T', 't':
		return BaseT, false, nil
	case 'N', 'n':
		return 0, true, nil
	default:
		return 0, false, fmt.Errorf("bclgen: invalid base %q in sequence %q (allowed: A/C/G/T/N)", string(ch), seq)
	}
}

// writeBCLFile generates a single .bcl.gz file for (lane, cycle, tile) using
// the seeded spec. The cluster bytes are derived from a per-cycle RNG so
// different cycles with the same spec are not byte-identical, but two runs of
// the same spec produce identical files.
//
// The cluster bytes are streamed directly through gzip (via writeBCLStream)
// so memory use is bounded regardless of spec.Clusters.
//
// If spec.Sequence is non-empty, each cycle uses the i'th character from
// the sequence (mod length) for ALL clusters, overriding the per-cluster
// random base. This is the path used for regression testing against
// bcl2fastq: you can verify that the FASTQ output matches a known pattern.
func writeBCLFile(out io.Writer, spec RunSpec, lane, cycle, tileNum int) error {
	return writeBCLStream(out, spec.Clusters, func(gz io.Writer) error {
		if spec.Sequence != "" {
			base, noCall, err := sequenceBase(spec.Sequence, cycle-1)
			if err != nil {
				return err
			}
			return writeConstantClusters(gz, spec.Clusters, base, noCall, spec.QScore)
		}
		return writeRandomClusters(gz, spec.Clusters, spec.QScore, spec.NoCallRate,
			spec.cycleRng(lane, cycle))
	})
}

// bclChunkSize is the streaming chunk size for both writeConstantClusters
// and writeRandomClusters. 4 KiB matches a typical filesystem block and
// gzip's internal buffer, so gzip.Writer.Write hits a hot path.
const bclChunkSize = 4096

// writeConstantClusters writes n identical cluster bytes to w. When noCall is
// true, every byte is 0x00 (no-call). Otherwise every byte is
// packClusterByte(base, qscore).
func writeConstantClusters(w io.Writer, n int, base byte, noCall bool, qscore int) error {
	b := byte(0x00)
	if !noCall {
		b = packClusterByte(base, qscore)
	}
	chunk := make([]byte, bclChunkSize)
	for i := range chunk {
		chunk[i] = b
	}
	remaining := n
	for remaining > 0 {
		if remaining < len(chunk) {
			chunk = chunk[:remaining]
		}
		if _, err := w.Write(chunk); err != nil {
			return fmt.Errorf("bclgen: write const clusters: %w", err)
		}
		remaining -= len(chunk)
	}
	return nil
}

// writeRandomClusters writes n random cluster bytes to w using rng. Each
// cluster byte is independently either 0x00 (no-call) with probability
// noCallRate, or packClusterByte(uniformBase, qscore). RNG calls happen in
// ascending cluster order, matching the old non-streaming path.
func writeRandomClusters(w io.Writer, n, qscore int, noCallRate float64, rng *rand.Rand) error {
	chunk := make([]byte, bclChunkSize)
	remaining := n
	for remaining > 0 {
		size := len(chunk)
		if remaining < size {
			size = remaining
		}
		for i := 0; i < size; i++ {
			if noCallRate > 0 && rng.Float64() < noCallRate {
				chunk[i] = 0x00
			} else {
				chunk[i] = packClusterByte(byte(rng.IntN(4)), qscore)
			}
		}
		if _, err := w.Write(chunk[:size]); err != nil {
			return fmt.Errorf("bclgen: write random clusters: %w", err)
		}
		remaining -= size
	}
	return nil
}

// writeBCLFileToDisk is the on-disk counterpart of writeBCLFile: it creates the
// cycle directory if needed and writes the gzipped BCL to its final path.
func writeBCLFileToDisk(spec RunSpec, lane, cycle, tileNum int) error {
	// Directory is pre-created by Generate.precreateDirs.
	dir := spec.cycleDir(lane, cycle)
	name := fmt.Sprintf("s_%d_%04d.bcl.gz", lane, tileNum)
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("bclgen: create %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if err := writeBCLFile(f, spec, lane, cycle, tileNum); err != nil {
		return fmt.Errorf("bclgen: write %s: %w", path, err)
	}
	return nil
}
