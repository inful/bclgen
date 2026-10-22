package bclgen

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// TestLocsFile_PerTileVariation is the locs counterpart of
// TestFilterFile_PerTileVariation: every tile must get its own cluster
// coordinates, not a byte-identical copy of lane 1 tile 1's.
func TestLocsFile_PerTileVariation(t *testing.T) {
	out := t.TempDir()
	spec := DefaultSpec()
	spec.Out = out
	spec.Lanes, spec.Swaths, spec.Tiles = 2, 2, 2
	spec.Clusters = 32
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
				p := filepath.Join(out, "Data", "Intensities",
					fmt.Sprintf("L%03d", lane), fmt.Sprintf("s_%d_%04d.locs", lane, tileNum))
				data, err := os.ReadFile(p)
				if err != nil {
					t.Fatalf("read locs: %v", err)
				}
				sum := sha256.Sum256(data)
				key := hex.EncodeToString(sum[:])
				if prev, dup := seen[key]; dup {
					t.Errorf("locs for lane=%d swath=%d tile=%d is byte-identical to %s", lane, swath, tile, prev)
				}
				seen[key] = fmt.Sprintf("lane=%d swath=%d tile=%d", lane, swath, tile)
				n++
			}
		}
	}
	if n != spec.Lanes*spec.Swaths*spec.Tiles {
		t.Errorf("checked %d locs files, want %d", n, spec.Lanes*spec.Swaths*spec.Tiles)
	}
}

func TestLocsFile_Size(t *testing.T) {
	spec := DefaultSpec()
	spec.Clusters = 100

	var buf bytes.Buffer
	if err := writeLocs(&buf, spec, 1, 1, 1); err != nil {
		t.Fatalf("writeLocs: %v", err)
	}
	// 12-byte header + 8 bytes per cluster.
	if got, want := buf.Len(), 12+8*spec.Clusters; got != want {
		t.Errorf("locs size = %d, want %d", got, want)
	}
}

func TestLocsFile_HeaderFields(t *testing.T) {
	// bcl2fastq validates field1==1 and field2==1.0; this test pins those.
	spec := DefaultSpec()
	spec.Clusters = 8
	spec.Seed = 5

	var buf bytes.Buffer
	if err := writeLocs(&buf, spec, 1, 1, 1); err != nil {
		t.Fatalf("writeLocs: %v", err)
	}
	data := buf.Bytes()
	if field1 := binary.LittleEndian.Uint32(data[0:4]); field1 != 1 {
		t.Errorf("field1 = %d, want 1", field1)
	}
	if field2 := math.Float32frombits(binary.LittleEndian.Uint32(data[4:8])); field2 != 1.0 {
		t.Errorf("field2 = %v, want 1.0", field2)
	}
	if n := binary.LittleEndian.Uint32(data[8:12]); n != uint32(spec.Clusters) {
		t.Errorf("N = %d, want %d", n, spec.Clusters)
	}
}

func TestLocsFile_Deterministic(t *testing.T) {
	run := func() []byte {
		spec := DefaultSpec()
		spec.Clusters = 50
		spec.Seed = 12345
		var buf bytes.Buffer
		if err := writeLocs(&buf, spec, 1, 1, 1); err != nil {
			t.Fatalf("writeLocs: %v", err)
		}
		return buf.Bytes()
	}
	a, b := run(), run()
	if !bytes.Equal(a, b) {
		t.Errorf("locs bytes differ across runs with same seed")
	}
}
