package bclgen

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestFilterFile_Layout(t *testing.T) {
	spec := DefaultSpec()
	spec.Clusters = 7
	spec.PFRate = 1.0

	var buf bytes.Buffer
	if err := writeFilter(&buf, spec); err != nil {
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
	if err := writeFilter(&buf, spec); err != nil {
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
		if err := writeFilter(&buf, spec); err != nil {
			t.Fatalf("writeFilter: %v", err)
		}
		return buf.Bytes()
	}
	a, b := run(13), run(13)
	if !bytes.Equal(a, b) {
		t.Errorf("two runs with the same seed produced different bytes")
	}
}