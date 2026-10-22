package bclgen

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestFilterFile_PerTileVariation guards against a regression where every
// .filter file in a run came out byte-identical because the RNG was seeded
// from spec.Seed alone with no tile dimension. With -pf-rate < 1, all tiles
// then failed the exact same clusters at the exact same positions, which no
// real run exhibits and which made -swaths/-tiles only multiply file names.
func TestFilterFile_PerTileVariation(t *testing.T) {
	out := t.TempDir()
	spec := DefaultSpec()
	spec.Out = out
	spec.Lanes, spec.Swaths, spec.Tiles = 2, 2, 2
	spec.Clusters = 64
	spec.PFRate = 0.5
	spec.Seed = 7

	if err := Generate(spec); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	seen := make(map[string]string)
	n := 0
	for lane := 1; lane <= spec.Lanes; lane++ {
		for swath := 1; swath <= spec.Swaths; swath++ {
			for tile := 1; tile <= spec.Tiles; tile++ {
				tileNum := spec.tileNumber(lane, swath, tile)
				p := filepath.Join(out, "Data", "Intensities", "BaseCalls",
					fmt.Sprintf("L%03d", lane), fmt.Sprintf("s_%d_%04d.filter", lane, tileNum))
				data, err := os.ReadFile(p)
				if err != nil {
					t.Fatalf("read filter: %v", err)
				}
				sum := sha256.Sum256(data)
				key := hex.EncodeToString(sum[:])
				if prev, dup := seen[key]; dup {
					t.Errorf("filter for lane=%d swath=%d tile=%d is byte-identical to %s", lane, swath, tile, prev)
				}
				seen[key] = fmt.Sprintf("lane=%d swath=%d tile=%d", lane, swath, tile)
				n++
			}
		}
	}
	if n != spec.Lanes*spec.Swaths*spec.Tiles {
		t.Errorf("checked %d filter files, want %d", n, spec.Lanes*spec.Swaths*spec.Tiles)
	}
}

func TestFilterFile_Layout(t *testing.T) {
	spec := DefaultSpec()
	spec.Clusters = 7
	spec.PFRate = 1.0

	var buf bytes.Buffer
	if err := writeFilter(&buf, spec, 1, 1, 1); err != nil {
		t.Fatalf("writeFilter: %v", err)
	}
	data := buf.Bytes()
	// bcl2fastq v2.20 header: 12 bytes (zero marker + version + N) + N cluster bytes.
	if len(data) != 12+7 {
		t.Fatalf("filter size = %d, want %d", len(data), 12+7)
	}
	if got := binary.LittleEndian.Uint32(data[0:4]); got != 0 {
		t.Errorf("marker = %d, want 0 (header present)", got)
	}
	if got := binary.LittleEndian.Uint32(data[4:8]); got != filterFileVersion {
		t.Errorf("version = %d, want %d", got, filterFileVersion)
	}
	if got := binary.LittleEndian.Uint32(data[8:12]); got != 7 {
		t.Errorf("N = %d, want 7", got)
	}
	for i, b := range data[12:] {
		if b&0x01 != 1 {
			t.Errorf("cluster %d not PF (byte=%#x)", i, b)
		}
	}
}

func TestFilterFile_PartialPFRate(t *testing.T) {
	spec := DefaultSpec()
	spec.Clusters = 1000
	spec.PFRate = 0.5
	spec.Seed = 99

	var buf bytes.Buffer
	if err := writeFilter(&buf, spec, 1, 1, 1); err != nil {
		t.Fatalf("writeFilter: %v", err)
	}
	data := buf.Bytes()
	pf := 0
	for _, b := range data[12:] {
		if b&0x01 == 1 {
			pf++
		}
	}
	// With seed 99 and 1000 clusters, expect roughly 500 PF. Allow 30% slack.
	if pf < 350 || pf > 650 {
		t.Errorf("PF count = %d, expected ~500 (allowed 350..650)", pf)
	}
}

func TestFilterFile_DeterministicBytes(t *testing.T) {
	// Two calls with the same seed → byte-identical .filter payloads.
	run := func(seed uint64) []byte {
		spec := DefaultSpec()
		spec.Seed = seed
		spec.PFRate = 0.7
		spec.Clusters = 64
		var buf bytes.Buffer
		if err := writeFilter(&buf, spec, 1, 1, 1); err != nil {
			t.Fatalf("writeFilter: %v", err)
		}
		return buf.Bytes()
	}
	a, b := run(13), run(13)
	if !bytes.Equal(a, b) {
		t.Errorf("two runs with the same seed produced different bytes")
	}
}
