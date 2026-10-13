//go:build debug_pattern

// Build with: go test -tags debug_pattern -run TestDebug -v ./internal/bclgen/
package bclgen

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// TestDebug_AllFF generates BCL files where every cluster byte is 0xFF
// (Q=63, base=3=T). If bcl2fastq reads these correctly it should produce
// FASTQ reads of all 'T' with Q='~' (Q=63 Phred+33 = 96 = '`'... wait let me
// recompute: '~' = 126, 126-33=93, so Q=93 for 0xFF? No, 0xFF>>2=63, 63+33=96='`'
// so Q=63, char '`').
func TestDebug_AllFF(t *testing.T) {
	out := t.TempDir()
	spec := DefaultSpec()
	spec.Out = out
	spec.Cycles = 2
	spec.Clusters = 4
	if err := Generate(spec); err != nil {
		t.Fatal(err)
	}
	// Overwrite cycle 1 BCL with all-0xFF clusters.
	p := filepath.Join(out, "Data", "Intensities", "BaseCalls", "L001", "C1.1", "s_1_1101.bcl.gz")
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	gw.Write([]byte{4, 0, 0, 0}) // N=4
	gw.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	gw.Close()
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote all-0xFF BCL to %s", p)
}
