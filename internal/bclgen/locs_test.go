package bclgen

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// bcl2fastq v2.20 stores locs files at <runfolder>/Data/Intensities/L<NNN>/,
// not under BaseCalls/. See PositionsFile.cpp.
const locsDir = "Data/Intensities/L001"

func TestLocsFile_Size(t *testing.T) {
	out := t.TempDir()
	spec := DefaultSpec()
	spec.Out = out
	spec.Clusters = 100

	if err := Generate(spec); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	path := filepath.Join(out, locsDir, "s_1_1101.locs")
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat locs: %v", err)
	}
	// 12-byte header + 8 bytes per cluster.
	if got, want := st.Size(), int64(12+8*spec.Clusters); got != want {
		t.Errorf("locs size = %d, want %d", got, want)
	}
}

func TestLocsFile_HeaderFields(t *testing.T) {
	// bcl2fastq validates field1==1 and field2==1.0; this test pins those.
	out := t.TempDir()
	spec := DefaultSpec()
	spec.Out = out
	spec.Clusters = 8
	spec.Seed = 5

	if err := Generate(spec); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(out, locsDir, "s_1_1101.locs"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
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
		out := t.TempDir()
		spec := DefaultSpec()
		spec.Out = out
		spec.Clusters = 50
		spec.Seed = 12345
		if err := Generate(spec); err != nil {
			t.Fatalf("Generate: %v", err)
		}
		data, err := os.ReadFile(filepath.Join(out, locsDir, "s_1_1101.locs"))
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		return data
	}
	a, b := run(), run()
	if !bytes.Equal(a, b) {
		t.Errorf("locs bytes differ across runs with same seed")
	}
}
