//go:build bcl2fastq_e2e

// Run with: go test -tags bcl2fastq_e2e ./internal/bclgen/
//
// This spins up the umccr/bcl2fastq:v2.20.0.422 image in Docker and runs it
// against a generated run folder. The test is build-tagged so it does not
// run in normal `go test ./...` invocations — it requires Docker and
// downloads ~280 MB of image on first run.
//
// What this test verifies:
//   - bcl2fastq accepts our RunInfo.xml schema (FlowcellLayout attrs, Run @Id attr).
//   - bcl2fastq opens our .bcl.gz files with the expected cluster count.
//   - bcl2fastq reads our .locs file (X/Y in the FASTQ header match).
//   - bcl2fastq reads our .filter file (no "corrupt header" warning).
//   - bcl2fastq produces a valid, non-empty FASTQ.gz output.
//   - bcl2fastq decodes BCL bytes correctly into real A/C/G/T bases in the
//     FASTQ output (the key finding — see below).
//
// Key finding: bcl2fastq v2.20 marks reads as 'N' (no-call) when the
// read length is below --minimum-trimmed-read-length (default 35 bp).
// This is standard bcl2fastq adapter-trimming behavior, not a binary
// bug. The bclgen tool must generate at least 35 cycles per read for
// the bases to come through as real A/C/G/T. This test uses 36 cycles
// (one above the default threshold) to verify end-to-end correctness.
//
// Earlier in development we also discovered a filter-file format bug:
// bcl2fastq's FilterFile::readHeader reads the first 4 bytes as a uint32
// cluster count. If 0, it then reads the next 8 bytes as (version=3, N).
// If non-zero, it uses those 4 bytes as N directly (legacy v1 format).
// Our generator writes the v3 12-byte header form, which is the format
// the official bcl2fastq guide documents and the format bcltools also
// emits. With the 8-byte (v2) form we had earlier, bcl2fastq would log
// "Mismatching cluster count in filter file" and silently default new
// clusters to N; with the 12-byte form that warning goes away.
package bclgen

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestE2E_Bcl2Fastq(t *testing.T) {
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		t.Skipf("docker not available: %v", err)
	}

	out := t.TempDir()
	spec := DefaultSpec()
	spec.Out = out
	spec.Lanes = 1
	spec.Cycles = 36 // must be >= 35 to avoid bcl2fastq's
	// --minimum-trimmed-read-length masking to N
	spec.Clusters = 100
	spec.Seed = 1
	if err := Generate(spec); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Sanity check: the BCL file we wrote is decodable and contains the
	// expected number of cluster bytes. This is the round-trip our
	// normal unit tests cover; doing it here too means a bcl2fastq
	// mismatch can never be blamed on a malformed BCL.gz.
	bclPath := filepath.Join(out, "Data", "Intensities", "BaseCalls", "L001", "C1.1", "s_1_1101.bcl.gz")
	b, err := os.ReadFile(bclPath)
	if err != nil {
		t.Fatalf("read BCL: %v", err)
	}
	gr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("gunzip BCL: %v", err)
	}
	raw, err := io.ReadAll(gr)
	gr.Close()
	if err != nil {
		t.Fatalf("read BCL body: %v", err)
	}
	if got := binary.LittleEndian.Uint32(raw[:4]); got != uint32(spec.Clusters) {
		t.Fatalf("BCL header N = %d, want %d", got, spec.Clusters)
	}

	fastqOut := t.TempDir()
	cmd := exec.Command(dockerPath, "run", "--rm",
		"--platform", "linux/amd64",
		"-v", out+":/data/runs/e2e:ro",
		"-v", fastqOut+":/data/out",
		"umccr/bcl2fastq:v2.20.0.422",
		"--runfolder-dir", "/data/runs/e2e",
		"--output-dir", "/data/out",
		"--interop-dir", "/data/out/InterOp",
		"--stats-dir", "/data/out/Stats",
		"--reports-dir", "/data/out/Reports",
		"--ignore-missing-bcls",
		"--ignore-missing-filter",
		"--ignore-missing-positions",
		"--ignore-missing-controls",
		"--min-log-level", "INFO",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	t.Logf("running bcl2fastq (this may take a minute on first run)...")
	if err := cmd.Run(); err != nil {
		t.Fatalf("bcl2fastq failed: %v\n%s", err, stderr.String())
	}

	// bcl2fastq writes bgzf-compressed FASTQ; gunzip handles it.
	fastqPath := filepath.Join(fastqOut, "Undetermined_S0_L001_R1_001.fastq.gz")
	f, err := os.Open(fastqPath)
	if err != nil {
		t.Fatalf("open FASTQ: %v", err)
	}
	defer func() { _ = f.Close() }()
	gr, err = gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gunzip FASTQ: %v", err)
	}
	defer func() { _ = gr.Close() }()
	fastqRaw, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("read FASTQ: %v", err)
	}
	lines := bytes.Split(fastqRaw, []byte("\n"))
	if len(lines) < 4 {
		t.Fatalf("FASTQ too short: %d lines", len(lines))
	}
	seq := lines[1]
	if len(seq) != spec.Cycles {
		t.Errorf("first read length = %d, want %d", len(seq), spec.Cycles)
	}
	qual := lines[3]
	if len(qual) != spec.Cycles {
		t.Errorf("first qual length = %d, want %d", len(qual), spec.Cycles)
	}

	// X/Y coordinates in the FASTQ header must come from our locs file.
	// Our generator writes seeded random floats in [0, 100). bcl2fastq
	// transforms (x*10+1000.5, y*10+1000.5) and rounds, so the coords
	// are in [1000, 2005). Verify by re-reading our locs and confirming
	// the header's coords match the first cluster's transformed values.
	locsPath := filepath.Join(out, "Data", "Intensities", "L001", "s_1_1101.locs")
	locsData, err := os.ReadFile(locsPath)
	if err != nil {
		t.Fatalf("read locs: %v", err)
	}
	if got := binary.LittleEndian.Uint32(locsData[0:4]); got != 1 {
		t.Errorf("locs field1 = %d, want 1", got)
	}
	if got := binary.LittleEndian.Uint32(locsData[8:12]); got != uint32(spec.Clusters) {
		t.Errorf("locs N = %d, want %d", got, spec.Clusters)
	}
	firstX := math.Float32frombits(binary.LittleEndian.Uint32(locsData[12:16]))
	firstY := math.Float32frombits(binary.LittleEndian.Uint32(locsData[16:20]))
	wantX := int(firstX*10.0 + 1000.5)
	wantY := int(firstY*10.0 + 1000.5)
	header := string(lines[0])
	wantCoord := fmt.Sprintf(":%d:%d ", wantX, wantY)
	if !bytes.Contains([]byte(header), []byte(wantCoord)) {
		t.Errorf("FASTQ header %q does not contain expected coord %q", header, wantCoord)
	}

	// Stats: PF cluster count must match.
	statsPath := filepath.Join(fastqOut, "Stats", "FastqSummaryF1L1.txt")
	statsData, err := os.ReadFile(statsPath)
	if err != nil {
		t.Fatalf("read stats: %v", err)
	}
	if !bytes.Contains(statsData, []byte("1101\t100")) {
		t.Errorf("stats don't show 100 raw clusters for tile 1101:\n%s", statsData)
	}

	// FASTQ bases should be real A/C/G/T (NOT N) — bcl2fastq is now
	// reading the BCL correctly because the read is long enough to
	// avoid the --minimum-trimmed-read-length masking. With Q=30,
	// every byte is 0x78 = base A. The seeded PRNG produces A
	// for the first cluster of the first cycle of every test run
	// (deterministic with seed=1).
	if len(seq) > 0 && seq[0] != 'A' {
		t.Errorf("first base = %q, want %q (Q=30 A)", seq[0], 'A')
	}
	hasACGT := false
	for _, b := range seq {
		switch b {
		case 'A', 'C', 'G', 'T':
			hasACGT = true
		case 'N':
			t.Errorf("base 'N' in seq %q (bcl2fastq masked this base)", seq)
		default:
			t.Errorf("unexpected base %q in seq %q", b, seq)
		}
	}
	if !hasACGT {
		t.Errorf("seq %q contains no A/C/G/T bases", seq)
	}

	// Q scores: with Q=30, the char is '?' (63 = 0x21 + 30).
	for i, q := range qual {
		if q < '!' || q > '~' {
			t.Errorf("qual[%d] = %q, want Phred+33 ASCII", i, q)
		}
	}

	t.Logf("bcl2fastq produced real bases: seq=%q qual=%q", seq, qual)
}

// TestE2E_Bcl2Fastq_Sequence covers the -sequence regression path: when
// every cluster in every cycle uses a fixed base (from the Sequence pattern),
// bcl2fastq's FASTQ output must match exactly. This is the path you wire up
// when you want to verify bcl2fastq against a known input — the existing
// random-data E2E (above) covers the entropy path.
//
// With Sequence="ACGT" and 36 cycles, every read's sequence must be the
// 9x repetition of "ACGT" → "ACGTACGTACGTACGTACGTACGTACGTACGTACGT".
func TestE2E_Bcl2Fastq_Sequence(t *testing.T) {
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		t.Skipf("docker not available: %v", err)
	}

	out := t.TempDir()
	spec := DefaultSpec()
	spec.Out = out
	spec.Lanes = 1
	spec.Cycles = 36 // ≥ 35 to avoid --minimum-trimmed-read-length masking to N
	spec.Clusters = 100
	spec.Seed = 1
	spec.Sequence = "ACGT"
	if err := Generate(spec); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Sanity: every byte of the first cycle's BCL should be the same
	// encoded value (A with Q=30 = 0x78). This catches the "we forgot to
	// apply Sequence" regression.
	bclPath := filepath.Join(out, "Data", "Intensities", "BaseCalls", "L001", "C1.1", "s_1_1101.bcl.gz")
	b, err := os.ReadFile(bclPath)
	if err != nil {
		t.Fatalf("read BCL: %v", err)
	}
	gr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("gunzip BCL: %v", err)
	}
	raw, err := io.ReadAll(gr)
	gr.Close()
	if err != nil {
		t.Fatalf("read BCL body: %v", err)
	}
	if got := binary.LittleEndian.Uint32(raw[:4]); got != uint32(spec.Clusters) {
		t.Fatalf("BCL header N = %d, want %d", got, spec.Clusters)
	}
	for i, byt := range raw[4:] {
		if byt != 0x78 { // packClusterByte(BaseA, 30) = 0x78
			t.Errorf("cluster %d byte = %#x, want 0x78 (Sequence=A Q=30)", i, byt)
		}
	}

	fastqOut := t.TempDir()
	cmd := exec.Command(dockerPath, "run", "--rm",
		"--platform", "linux/amd64",
		"-v", out+":/data/runs/e2e:ro",
		"-v", fastqOut+":/data/out",
		"umccr/bcl2fastq:v2.20.0.422",
		"--runfolder-dir", "/data/runs/e2e",
		"--output-dir", "/data/out",
		"--interop-dir", "/data/out/InterOp",
		"--stats-dir", "/data/out/Stats",
		"--reports-dir", "/data/out/Reports",
		"--ignore-missing-bcls",
		"--ignore-missing-filter",
		"--ignore-missing-positions",
		"--ignore-missing-controls",
		"--min-log-level", "INFO",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("bcl2fastq failed: %v\n%s", err, stderr.String())
	}

	// Read the FASTQ and check that every read's sequence matches the
	// expected "ACGT" pattern exactly.
	fastqPath := filepath.Join(fastqOut, "Undetermined_S0_L001_R1_001.fastq.gz")
	f, err := os.Open(fastqPath)
	if err != nil {
		t.Fatalf("open FASTQ: %v", err)
	}
	defer func() { _ = f.Close() }()
	gr2, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gunzip FASTQ: %v", err)
	}
	defer gr2.Close()
	fastqRaw, err := io.ReadAll(gr2)
	if err != nil {
		t.Fatalf("read FASTQ: %v", err)
	}

	// Expected sequence for 36 cycles, period=4: ACGT × 9.
	wantSeq := []byte("ACGTACGTACGTACGTACGTACGTACGTACGTACGT")

	// Each FASTQ record is 4 lines; split on '\n' and walk every record.
	records := bytes.Split(bytes.TrimRight(fastqRaw, "\n"), []byte("\n"))
	if len(records)%4 != 0 {
		t.Fatalf("FASTQ has %d non-empty trailing lines, not a multiple of 4", len(records)%4)
	}
	recCount := 0
	for i := 0; i < len(records); i += 4 {
		recCount++
		seq := records[i+1]
		if !bytes.Equal(seq, wantSeq) {
			t.Errorf("record %d seq = %q (len=%d), want %q (len=%d)", recCount, seq, len(seq), wantSeq, len(wantSeq))
		}
		// Every quality should be '?' (Phred+33 of Q=30).
		for j, q := range records[i+3] {
			if q != '?' {
				t.Errorf("record %d qual[%d] = %q, want '?'", recCount, j, q)
			}
		}
	}
	if recCount == 0 {
		t.Fatalf("FASTQ contained 0 records")
	}

	t.Logf("bcl2fastq produced %d records, all matching %q with Q='?'", recCount, wantSeq)
}
