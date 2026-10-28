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
	"strings"
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
	err := Generate(spec)
	if err == nil {
		t.Fatalf("expected error when Out is non-empty, got nil")
	}
	// The refusal should point at the escape hatch.
	if !strings.Contains(err.Error(), "-force") {
		t.Errorf("refusal should mention -force, got: %v", err)
	}
	// And it must not have touched the existing content.
	if data, readErr := os.ReadFile(filepath.Join(out, "hello.txt")); readErr != nil || string(data) != "nope" {
		t.Errorf("existing content was modified by a refused run: %q %v", data, readErr)
	}
}

// TestGenerate_RefusesLockedOutputDir covers the concurrency guard. Two
// bclgen processes aimed at the same -out otherwise interleave their writes
// and leave a folder that is a mixture of both runs, with both processes
// reporting success.
// TestGenerate_FillDefaultsFromBareSpec covers the library path where a
// caller hand-builds a RunSpec instead of starting from DefaultSpec(). Every
// other test starts from DefaultSpec(), so the defaulting branches for the
// string fields were never exercised.
//
// It also pins the exception: PFRate and NoCallRate are float64 and 0 is a
// meaningful value for them, so zeroing must survive fillDefaults rather
// than being read back as "unset". TestGenerate_AcceptsZeroPFRate covers the
// PFRate half through Generate; this checks both directly.
func TestGenerate_FillDefaultsFromBareSpec(t *testing.T) {
	out := t.TempDir()
	spec := RunSpec{Out: out} // deliberately bare
	filled := spec.fillDefaults()
	d := DefaultSpec()

	if filled.Lanes != d.Lanes || filled.Swaths != d.Swaths || filled.Tiles != d.Tiles ||
		filled.Surfaces != d.Surfaces || filled.Cycles != d.Cycles ||
		filled.Clusters != d.Clusters || filled.QScore != d.QScore || filled.Seed != d.Seed {
		t.Errorf("numeric fields not defaulted: %+v", filled)
	}
	if filled.RunID != d.RunID || filled.Instrument != d.Instrument ||
		filled.Date != d.Date || filled.Flowcell != d.Flowcell {
		t.Errorf("string fields not defaulted: RunID=%q Instrument=%q Date=%q Flowcell=%q",
			filled.RunID, filled.Instrument, filled.Date, filled.Flowcell)
	}
	// The float exception: 0 must survive.
	if filled.PFRate != 0 {
		t.Errorf("PFRate 0 was reset to %v; 0 means 'no cluster passes filter', not 'unset'", filled.PFRate)
	}
	if filled.NoCallRate != 0 {
		t.Errorf("NoCallRate 0 was reset to %v; 0 means 'no no-calls', not 'unset'", filled.NoCallRate)
	}

	// And the bare spec must actually generate a valid folder.
	if err := Generate(RunSpec{Out: filepath.Join(out, "run")}); err != nil {
		t.Fatalf("Generate with a bare RunSpec: %v", err)
	}
	if err := fileNonEmpty(t, filepath.Join(out, "run", "RunInfo.xml")); err != nil {
		t.Errorf("%v", err)
	}
}

// TestGenerate_RefusesLockedOutputDir covers the concurrency guard. Two
// bclgen processes aimed at the same -out otherwise interleave their writes
// and leave a folder that is a mixture of both runs, with both processes
// reporting success.
func TestGenerate_RefusesLockedOutputDir(t *testing.T) {
	out := filepath.Join(t.TempDir(), "run")

	// Simulate another bclgen holding the lock.
	lockPath := out + ".lock"
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	spec := DefaultSpec()
	spec.Out = out
	spec.Cycles = 2
	spec.Clusters = 8
	err := Generate(spec)
	if err == nil {
		t.Fatalf("expected Generate to refuse a locked output directory")
	}
	if !strings.Contains(err.Error(), "locked") {
		t.Errorf("error should say the directory is locked, got: %v", err)
	}
	// Nothing should have been written.
	if _, statErr := os.Stat(out); statErr == nil {
		t.Errorf("output directory was created despite the lock")
	}
}

// TestGenerate_ForceRefusesDangerousTargets covers the -force safety guard.
// os.RemoveAll("/") cannot remove the mount point itself, but it does remove
// every file beneath it — so 'bclgen -out / -force' would empty the disk.
// '..' would delete the parent directory. Both must be refused.
func TestGenerate_ForceRefusesDangerousTargets(t *testing.T) {
	for _, target := range []string{"/", ".", ".."} {
		t.Run(target, func(t *testing.T) {
			spec := DefaultSpec()
			spec.Out = target
			spec.Force = true
			spec.Cycles = 1
			spec.Clusters = 4
			err := Generate(spec)
			if err == nil {
				t.Fatalf("Generate accepted -force on %q", target)
			}
			if !strings.Contains(err.Error(), "-force") {
				t.Errorf("error should mention -force, got: %v", err)
			}
		})
	}
}

// TestGenerate_ForceReplacesPartialOutput covers the retry path: a run that
// died partway leaves a partial folder, and without -force every subsequent
// retry fails with "is not empty" until the user deletes it by hand.
func TestGenerate_ForceReplacesPartialOutput(t *testing.T) {
	out := t.TempDir()
	spec := DefaultSpec()
	spec.Out = out
	spec.Cycles = 2
	spec.Clusters = 8
	if err := Generate(spec); err != nil {
		t.Fatalf("first Generate: %v", err)
	}
	// Simulate a crash: drop one file and leave a stray from the partial run.
	if err := os.Remove(filepath.Join(out, "RunInfo.xml")); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(out, "Data", "Intensities", "BaseCalls", "L001", "C1.1", "stale.bcl.gz")
	if err := os.WriteFile(stray, []byte("leftover"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Without Force the retry is refused.
	if err := Generate(spec); err == nil {
		t.Fatalf("expected retry without Force to be refused")
	}

	// With Force the partial folder is replaced.
	spec.Force = true
	if err := Generate(spec); err != nil {
		t.Fatalf("Generate with Force: %v", err)
	}
	if _, err := os.Stat(stray); err == nil {
		t.Errorf("Force left a stale file behind at %s", stray)
	}
	if _, err := os.Stat(filepath.Join(out, "RunInfo.xml")); err != nil {
		t.Errorf("Force did not regenerate RunInfo.xml: %v", err)
	}
	// And the result is a complete, valid run folder.
	if err := fileNonEmpty(t, filepath.Join(out, "Data", "Intensities", "BaseCalls", "L001", "C2.1", "s_1_1101.bcl.gz")); err != nil {
		t.Errorf("%v", err)
	}
}

func TestGenerate_Deterministic(t *testing.T) {
	// Two identical runs produce byte-identical run folders. Pin Date
	// explicitly: DefaultSpec() reads time.Now() and the tree hash
	// includes the Date in RunInfo.xml, so a midnight-crossing test
	// run would compare different dates and flake.
	run := func() string {
		out := t.TempDir()
		spec := DefaultSpec()
		spec.Out = out
		spec.Cycles = 3
		spec.Clusters = 32
		spec.Seed = 2024
		spec.Date = "260101"
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
	// Above the 32-bit file-header limit: Generate must reject this
	// before writing anything, not after writing Clusters*9 bytes.
	// Out deliberately does not exist yet, so we can assert that
	// nothing was created on the way to failing.
	notYet := filepath.Join(t.TempDir(), "run")
	spec = DefaultSpec()
	spec.Out = notYet
	spec.Clusters = 0x100000000
	if err := Generate(spec); err == nil {
		t.Errorf("expected validation error for Clusters above the 32-bit header limit")
	} else if _, statErr := os.Stat(notYet); statErr == nil {
		t.Errorf("Validate rejected Clusters but the output directory was already created")
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
