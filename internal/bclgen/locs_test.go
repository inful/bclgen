package bclgen

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

func TestLocsFile_Size(t *testing.T) {
	spec := DefaultSpec()
	spec.Clusters = 100

	var buf bytes.Buffer
	if err := writeLocs(&buf, spec); err != nil {
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
	if err := writeLocs(&buf, spec); err != nil {
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
		if err := writeLocs(&buf, spec); err != nil {
			t.Fatalf("writeLocs: %v", err)
		}
		return buf.Bytes()
	}
	a, b := run(), run()
	if !bytes.Equal(a, b) {
		t.Errorf("locs bytes differ across runs with same seed")
	}
}
