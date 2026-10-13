package bclgen

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestFilterFile_Layout(t *testing.T) {
	out := t.TempDir()
	spec := DefaultSpec()
	spec.Out = out
	spec.Clusters = 7
	spec.PFRate = 1.0

	if err := Generate(spec); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	path := filepath.Join(out, "Data", "Intensities", "BaseCalls", "L001", "s_1_1101.filter")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read filter: %v", err)
	}
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
	out := t.TempDir()
	spec := DefaultSpec()
	spec.Out = out
	spec.Clusters = 1000
	spec.PFRate = 0.5
	spec.Seed = 99

	if err := Generate(spec); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	path := filepath.Join(out, "Data", "Intensities", "BaseCalls", "L001", "s_1_1101.filter")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read filter: %v", err)
	}
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
	// Two runs with the same seed → byte-identical .filter files.
	run := func(seed uint64) []byte {
		out := t.TempDir()
		spec := DefaultSpec()
		spec.Out = out
		spec.Seed = seed
		spec.PFRate = 0.7
		spec.Clusters = 64
		if err := Generate(spec); err != nil {
			t.Fatalf("Generate: %v", err)
		}
		data, err := os.ReadFile(filepath.Join(out, "Data", "Intensities", "BaseCalls", "L001", "s_1_1101.filter"))
		if err != nil {
			t.Fatalf("read filter: %v", err)
		}
		return data
	}
	a, b := run(13), run(13)
	if !bytes.Equal(a, b) {
		t.Errorf("two runs with the same seed produced different bytes")
	}
}
