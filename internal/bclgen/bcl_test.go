package bclgen

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// packClusterByte and decodeClusterByte live in bcl.go (production code).
// Tests just exercise them.

func TestPackClusterByte(t *testing.T) {
	cases := []struct {
		name string
		base byte
		q    int
		want byte
	}{
		{"A Q30", BaseA, 30, 30 << 2},
		{"C Q30", BaseC, 30, 30<<2 | 1},
		{"G Q30", BaseG, 30, 30<<2 | 2},
		{"T Q30", BaseT, 30, 30<<2 | 3},
		{"Q0", BaseA, 0, 0x00}, // ambiguous: treated as no call by bcl2fastq
		{"Q40", BaseA, 40, 40 << 2},
		{"Q63 max", BaseA, 63, 63 << 2},
		{"Q negative clamps to 0", BaseA, -5, 0x00},
		{"Q too high clamps to 63", BaseA, 999, 63 << 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := packClusterByte(c.base, c.q)
			if got != c.want {
				t.Fatalf("pack(%d,%d) = %#x, want %#x", c.base, c.q, got, c.want)
			}
		})
	}
}

func TestEncodeBCL_GZFormat(t *testing.T) {
	// Build a deterministic cluster byte stream of N=5 clusters.
	const n = 5
	clusters := []byte{
		packClusterByte(BaseA, 30),
		packClusterByte(BaseC, 30),
		packClusterByte(BaseG, 30),
		packClusterByte(BaseT, 30),
		0x00, // explicit no call
	}

	var buf bytes.Buffer
	if err := encodeBCL(&buf, n, clusters); err != nil {
		t.Fatalf("encodeBCL: %v", err)
	}

	// Stream is gzip-compressed.
	gr, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	raw, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("gzip read: %v", err)
	}
	if err := gr.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	if got := binary.LittleEndian.Uint32(raw[:4]); got != n {
		t.Fatalf("cluster count header = %d, want %d", got, n)
	}
	gotClusters := raw[4:]
	if !bytes.Equal(gotClusters, clusters) {
		t.Fatalf("clusters = %v, want %v", gotClusters, clusters)
	}
}

func TestEncodeBCL_RoundTrip(t *testing.T) {
	// Generate with the seeded path; then read back and verify every byte.
	spec := DefaultSpec()
	spec.Out = t.TempDir()
	spec.Clusters = 64
	spec.QScore = 30
	spec.Seed = 42

	// Arbitrary but seeded cluster bytes; this test exercises the
	// encodeBCL/readBCL round-trip, not the RNG's exact output.
	rng := spec.cycleRng(1, 1)
	want := make([]byte, spec.Clusters)
	for i := range want {
		base := byte(rng.IntN(4))
		want[i] = packClusterByte(base, spec.QScore)
	}

	var buf bytes.Buffer
	if err := encodeBCL(&buf, spec.Clusters, want); err != nil {
		t.Fatalf("encodeBCL: %v", err)
	}

	got, err := readBCL(&buf, spec.Clusters)
	if err != nil {
		t.Fatalf("readBCL: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("round-trip mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestEncodeBCL_Deterministic(t *testing.T) {
	// Same spec twice → byte-identical compressed output.
	mk := func() []byte {
		spec := DefaultSpec()
		spec.Out = t.TempDir()
		spec.Clusters = 256
		spec.Seed = 7
		var buf bytes.Buffer
		if err := writeBCLFile(&buf, spec, 1, 1); err != nil {
			t.Fatalf("writeBCLFile: %v", err)
		}
		return buf.Bytes()
	}
	a, b := mk(), mk()
	if !bytes.Equal(a, b) {
		t.Fatalf("two runs with the same spec produced different bytes")
	}
}

func TestWriteBCLFile_CreatesDirectoryAndFile(t *testing.T) {
	out := t.TempDir()
	spec := DefaultSpec()
	spec.Out = out
	spec.Cycles = 2
	spec.Clusters = 4

	if err := Generate(spec); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// File path: <out>/Data/Intensities/BaseCalls/L001/C1.1/s_1_1101.bcl.gz
	want := filepath.Join(out, "Data", "Intensities", "BaseCalls", "L001", "C1.1", "s_1_1101.bcl.gz")
	st, err := os.Stat(want)
	if err != nil {
		t.Fatalf("expected file %s: %v", want, err)
	}
	if st.Size() == 0 {
		t.Fatalf("%s is empty", want)
	}
}

// TestWriteBCLFile_DifferentCyclesAreDifferent guards against a regression
// where every BCL file in a run came out byte-identical because the per-cycle
// RNG was re-seeded the same way on every call. bcl2fastq reads each cycle
// independently, so they MUST contain different bases.
func TestWriteBCLFile_DifferentCyclesAreDifferent(t *testing.T) {
	out := t.TempDir()
	spec := DefaultSpec()
	spec.Out = out
	spec.Cycles = 3
	spec.Clusters = 32
	spec.Seed = 7

	if err := Generate(spec); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	read := func(cycle int) []byte {
		p := filepath.Join(out, "Data", "Intensities", "BaseCalls", "L001",
			fmt.Sprintf("C%d.1", cycle), "s_1_1101.bcl.gz")
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read cycle %d: %v", cycle, err)
		}
		return data
	}
	c1 := read(1)
	c2 := read(2)
	c3 := read(3)
	if bytes.Equal(c1, c2) {
		t.Errorf("cycle 1 and cycle 2 BCL files are byte-identical (regression)")
	}
	if bytes.Equal(c2, c3) {
		t.Errorf("cycle 2 and cycle 3 BCL files are byte-identical (regression)")
	}
}

func TestEncodeBCL_SequencePattern(t *testing.T) {
	// When Sequence is set, each cycle's base comes from the i'th character
	// of the sequence (mod length), for ALL clusters in that cycle.
	// With "ACGT" and 4 cycles, each cluster has bases A, C, G, T in order.
	// With Q=37, the expected byte is (37 << 2) | base = 0x94, 0x95, 0x96, 0x97.
	readAt := func(t *testing.T, cycle int, seq string) []byte {
		t.Helper()
		spec := DefaultSpec()
		spec.Out = t.TempDir()
		spec.Clusters = 4
		spec.Seed = 1
		spec.Sequence = seq
		spec.QScore = 37
		path := filepath.Join(spec.Out, "Data", "Intensities", "BaseCalls", "L001",
			fmt.Sprintf("C%d.1", cycle), "s_1_1101.bcl.gz")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		if err := writeBCLFile(f, spec, 1, cycle); err != nil {
			t.Fatal(err)
		}
		f2, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f2.Close() }()
		clusters, err := readBCL(f2, 4)
		if err != nil {
			t.Fatalf("readBCL: %v", err)
		}
		return clusters
	}

	// Cycle 1 -> 'A' -> 0x94, Cycle 2 -> 'C' -> 0x95,
	// Cycle 3 -> 'G' -> 0x96, Cycle 4 -> 'T' -> 0x97, Cycle 5 -> wraps to 'A' -> 0x94
	cases := map[int]byte{1: 0x94, 2: 0x95, 3: 0x96, 4: 0x97, 5: 0x94}
	for cycle, want := range cases {
		got := readAt(t, cycle, "ACGT")
		for i, b := range got {
			if b != want {
				t.Errorf("cycle %d cluster %d: got %#x, want %#x (sequence ACGT)", cycle, i, b, want)
			}
		}
	}
}

func TestEncodeBCL_SequenceNoCall(t *testing.T) {
	// 'N' in the sequence produces a no-call (byte 0x00).
	spec := DefaultSpec()
	spec.Out = t.TempDir()
	spec.Clusters = 2
	spec.Seed = 1
	spec.Sequence = "N"
	spec.QScore = 30
	path := filepath.Join(spec.Out, "Data", "Intensities", "BaseCalls", "L001",
		"C1.1", "s_1_1101.bcl.gz")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := writeBCLFile(f, spec, 1, 1); err != nil {
		t.Fatal(err)
	}
	f2, _ := os.Open(path)
	defer func() { _ = f2.Close() }()
	clusters, err := readBCL(f2, 2)
	if err != nil {
		t.Fatal(err)
	}
	for i, b := range clusters {
		if b != 0x00 {
			t.Errorf("cluster %d: got %#x, want 0x00 (no-call)", i, b)
		}
	}
}

func TestValidate_RejectsBadSequence(t *testing.T) {
	spec := DefaultSpec()
	spec.Out = t.TempDir()
	spec.Sequence = "ACXG" // X is not a valid base
	if err := spec.Validate(); err == nil {
		t.Error("expected Validate to reject sequence with X")
	}
}

// TestParseBase pins the single source of truth for the Illumina base
// alphabet. spec.Validate and sequenceBase both delegate here; a bug in
// this function would silently corrupt every BCL byte in the universe
// while still passing byte-equality determinism tests (because the
// change would be consistent across runs).
func TestParseBase(t *testing.T) {
	cases := []struct {
		ch       byte
		wantBase byte
		wantNo   bool
		wantOK   bool
	}{
		// Valid bases — both cases.
		{'A', BaseA, false, true}, {'a', BaseA, false, true},
		{'C', BaseC, false, true}, {'c', BaseC, false, true},
		{'G', BaseG, false, true}, {'g', BaseG, false, true},
		{'T', BaseT, false, true}, {'t', BaseT, false, true},
		{'N', 0, true, true}, {'n', 0, true, true},
		// Invalid characters — the four categories that real users hit.
		{'X', 0, false, false}, // other letter (the TestValidate case)
		{'B', 0, false, false}, // IUPAC ambiguity code
		{'U', 0, false, false}, // RNA — explicitly out of scope
		{'0', 0, false, false}, // digit
		{' ', 0, false, false}, // whitespace
	}
	for _, c := range cases {
		base, noCall, ok := parseBase(c.ch)
		if base != c.wantBase || noCall != c.wantNo || ok != c.wantOK {
			t.Errorf("parseBase(%q) = (%#x, %v, %v), want (%#x, %v, %v)",
				c.ch, base, noCall, ok, c.wantBase, c.wantNo, c.wantOK)
		}
	}
}

// TestWriteRandomClusters_NoCallRate_ShortCircuit pins the RNG call order
// in writeRandomClusters. The 'noCallRate > 0 &&' short-circuit decides
// whether Float64 is consumed per cluster: with noCallRate==0 the RNG
// only consumes IntN(4); with noCallRate>0 it consumes Float64 (and
// sometimes IntN(4) too). Getting the condition order wrong would
// silently break determinism — same seed would produce different bytes
// when noCallRate changes — while still passing the current
// TestGenerate_Deterministic because that test only exercises
// noCallRate==0.
func TestWriteRandomClusters_NoCallRate_ShortCircuit(t *testing.T) {
	mk := func(noCallRate float64) []byte {
		spec := DefaultSpec()
		spec.Clusters = 256
		spec.NoCallRate = noCallRate
		spec.Seed = 1
		var buf bytes.Buffer
		if err := writeRandomClusters(&buf, spec.Clusters, spec.QScore,
			noCallRate, spec.cycleRng(1, 1)); err != nil {
			t.Fatalf("writeRandomClusters: %v", err)
		}
		return buf.Bytes()
	}
	// Two calls with the same seed + same noCallRate produce the same
	// bytes (within-stream determinism).
	a := mk(0)
	b := mk(0)
	if !bytes.Equal(a, b) {
		t.Errorf("noCallRate=0: two calls with the same seed produced different bytes")
	}
	c := mk(0.5)
	d := mk(0.5)
	if !bytes.Equal(c, d) {
		t.Errorf("noCallRate=0.5: two calls with the same seed produced different bytes")
	}
	// Different noCallRate → different byte output (the RNG state diverges
	// because the Float64 calls change the consumption pattern).
	if bytes.Equal(a, c) {
		t.Errorf("noCallRate=0 and noCallRate=0.5 produced identical bytes — the short-circuit is broken")
	}
}
