package bclgen

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestGenerate_Layout walks the run folder bclgen produced and verifies the
// expected files exist with non-zero size, for a non-trivial spec.
func TestGenerate_Layout(t *testing.T) {
	out := t.TempDir()
	spec := DefaultSpec()
	spec.Out = out
	spec.Lanes = 2
	spec.Swaths = 1
	spec.Tiles = 2
	spec.Cycles = 4
	spec.Surfaces = 2 // flow cell has 2 surfaces, but .bcl.gz always writes surface 1
	spec.Clusters = 16
	spec.Seed = 7

	if err := Generate(spec); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, err := os.Stat(filepath.Join(out, "RunInfo.xml")); err != nil {
		t.Errorf("missing RunInfo.xml: %v", err)
	}

	for lane := 1; lane <= 2; lane++ {
		for swath := 1; swath <= 1; swath++ {
			for tile := 1; tile <= 2; tile++ {
				// bcl2fastq: tile = tile_within_swath + swath*100 + surface*1000
				// with surface=1 for .bcl.gz.
				tileNum := 1*1000 + swath*100 + tile
				laneDir := fmt.Sprintf("L%03d", lane)
				tileName := fmt.Sprintf("s_%d_%04d", lane, tileNum)
				// .locs lives at <runfolder>/Data/Intensities/L<NNN>/ (one level
				// shallower than the BCL/filter files).
				if p := filepath.Join(out, "Data", "Intensities", laneDir, tileName+".locs"); fileNonEmpty(t, p) != nil {
					t.Errorf("%v", fileNonEmpty(t, p))
				}
				// .filter lives next to the BCL files under BaseCalls/.
				if p := filepath.Join(out, "Data", "Intensities", "BaseCalls", laneDir, tileName+".filter"); fileNonEmpty(t, p) != nil {
					t.Errorf("%v", fileNonEmpty(t, p))
				}
				// Always surface 1 for .bcl.gz, regardless of spec.Surfaces.
				for cycle := 1; cycle <= 4; cycle++ {
					p := filepath.Join(out, "Data", "Intensities", "BaseCalls", laneDir,
						fmt.Sprintf("C%d.1", cycle), tileName+".bcl.gz")
					if err := fileNonEmpty(t, p); err != nil {
						t.Errorf("%v", err)
					}
				}
			}
		}
	}
}

func fileNonEmpty(t *testing.T, p string) error {
	t.Helper()
	st, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("expected non-empty %s: %v", p, err)
	}
	if st.Size() == 0 {
		return fmt.Errorf("expected non-empty %s: size=0", p)
	}
	return nil
}

func TestGenerate_RefusesNonEmptyDir(t *testing.T) {
	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(out, "hello.txt"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := DefaultSpec()
	spec.Out = out
	if err := Generate(spec); err == nil {
		t.Fatalf("expected error when Out is non-empty, got nil")
	}
}

func TestGenerate_Deterministic(t *testing.T) {
	// Two identical runs produce byte-identical run folders.
	run := func() string {
		out := t.TempDir()
		spec := DefaultSpec()
		spec.Out = out
		spec.Cycles = 3
		spec.Clusters = 32
		spec.Seed = 2024
		if err := Generate(spec); err != nil {
			t.Fatalf("Generate: %v", err)
		}
		return out
	}
	a, b := run(), run()
	if treeHash(t, a) != treeHash(t, b) {
		t.Fatalf("two runs with the same spec produced different folder contents")
	}
}

func TestGenerate_RoundTrip_BCLContents(t *testing.T) {
	// Open one of the BCL.gz files, decode it, and check that base frequencies
	// and Q-scores look reasonable.
	out := t.TempDir()
	spec := DefaultSpec()
	spec.Out = out
	spec.Cycles = 1
	spec.Clusters = 200
	spec.QScore = 30
	spec.Seed = 11

	if err := Generate(spec); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	path := filepath.Join(out, "Data", "Intensities", "BaseCalls", "L001", "C1.1", "s_1_1101.bcl.gz")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = f.Close() }()
	clusters, err := readBCL(f, spec.Clusters)
	if err != nil {
		t.Fatalf("readBCL: %v", err)
	}

	var counts [4]int
	for _, c := range clusters {
		base, q, _ := decodeClusterByte(c)
		if q != spec.QScore {
			t.Errorf("cluster byte %#x: Q=%d, want %d", c, q, spec.QScore)
		}
		counts[base]++
	}
	// Uniform distribution; allow ±3 standard deviations of ~sqrt(50)≈7.
	for b, n := range counts {
		if n < 30 || n > 70 {
			t.Errorf("base %d count = %d, expected ~50", b, n)
		}
	}
}

func TestGenerate_HandlesValidationErrors(t *testing.T) {
	spec := DefaultSpec()
	spec.Out = t.TempDir()
	spec.Lanes = -1
	if err := Generate(spec); err == nil {
		t.Errorf("expected validation error for Lanes=-1")
	}
	spec = DefaultSpec()
	spec.Out = t.TempDir()
	spec.Lanes = 1000
	if err := Generate(spec); err == nil {
		t.Errorf("expected validation error for Lanes=1000 (would overflow L%%03d)")
	}
	spec = DefaultSpec()
	spec.Out = t.TempDir()
	spec.Surfaces = 7
	if err := Generate(spec); err == nil {
		t.Errorf("expected validation error for Surfaces=7")
	}
	spec = DefaultSpec()
	spec.Out = t.TempDir()
	spec.Swaths = 100
	if err := Generate(spec); err == nil {
		t.Errorf("expected validation error for Swaths=100 (would overflow %%04d tile id)")
	}
	spec = DefaultSpec()
	spec.Out = t.TempDir()
	spec.Tiles = 100
	if err := Generate(spec); err == nil {
		t.Errorf("expected validation error for Tiles=100 (would overflow %%04d tile id)")
	}
	spec = DefaultSpec()
	spec.Out = ""
	if err := Generate(spec); err == nil {
		t.Errorf("expected validation error for empty Out")
	}
}

// TestGenerate_AcceptsZeroPFRate documents the API contract: PFRate == 0 means
// "no clusters pass filter" and must NOT be auto-bumped to 1.0.
func TestGenerate_AcceptsZeroPFRate(t *testing.T) {
	out := t.TempDir()
	spec := DefaultSpec()
	spec.Out = out
	spec.Clusters = 32
	spec.PFRate = 0

	if err := Generate(spec); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(out, "Data", "Intensities", "BaseCalls", "L001", "s_1_1101.filter"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for i, b := range data[12:] {
		if b&0x01 != 0 {
			t.Errorf("cluster %d unexpectedly passed filter: byte=%#x", i, b)
		}
	}
}

// treeHash returns a stable fingerprint of every file under root. .bcl.gz
// files are gunzipped first so the gzip header timestamp doesn't break
// determinism across runs.
func treeHash(t *testing.T, root string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		h.Write([]byte(rel))
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if filepath.Ext(p) == ".gz" {
			gr, err := gzip.NewReader(bytes.NewReader(data))
			if err != nil {
				return err
			}
			defer func() { _ = gr.Close() }()
			raw, err := io.ReadAll(gr)
			if err != nil {
				return err
			}
			data = raw
		}
		h.Write(data)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return hex.EncodeToString(h.Sum(nil))
}
