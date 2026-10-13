package bclgen

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"testing"
)

func TestRunInfo_Generated(t *testing.T) {
	out := t.TempDir()
	spec := DefaultSpec()
	spec.Out = out
	spec.Lanes = 2
	spec.Surfaces = 2
	spec.Swaths = 3
	spec.Tiles = 4
	spec.Cycles = 50
	spec.RunID = "abc"
	spec.Instrument = "X1"
	spec.Date = "240101"
	spec.Flowcell = "FC42"

	if err := Generate(spec); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "RunInfo.xml"))
	if err != nil {
		t.Fatalf("read RunInfo: %v", err)
	}
	var ri runInfo
	if err := xml.Unmarshal(data, &ri); err != nil {
		t.Fatalf("parse RunInfo: %v", err)
	}
	if ri.Version != "2" {
		t.Errorf("Version = %q, want 2", ri.Version)
	}
	if ri.Run.RunId != "abc" {
		t.Errorf("RunId = %q, want abc", ri.Run.RunId)
	}
	if ri.Run.Number != 1 {
		t.Errorf("Run.Number = %d, want 1", ri.Run.Number)
	}
	if ri.Run.Instrument != "X1" || ri.Run.Date != "240101" || ri.Run.Flowcell != "FC42" {
		t.Errorf("metadata mismatch: %+v", ri.Run)
	}
	if len(ri.Run.Reads.Reads) != 1 {
		t.Fatalf("Reads = %d, want 1", len(ri.Run.Reads.Reads))
	}
	r := ri.Run.Reads.Reads[0]
	if r.Number != 1 || r.NumCycles != 50 || r.IsIndexed != "N" {
		t.Errorf("Read = %+v", r)
	}
	if ri.Run.Layout.LaneCount != 2 || ri.Run.Layout.SurfaceCount != 2 ||
		ri.Run.Layout.SwathCount != 3 || ri.Run.Layout.TileCount != 4 {
		t.Errorf("Layout = %+v", ri.Run.Layout)
	}

	// The raw XML must have FlowcellLayout as a self-closing element with
	// attributes — that is the shape bcl2fastq v2.20's RunInfoXml.cpp expects.
	xmlStr := string(data)
	for _, want := range []string{
		`Id="abc"`,
		`<FlowcellLayout LaneCount="2" SurfaceCount="2" SwathCount="3" TileCount="4"`,
	} {
		if !contains(xmlStr, want) {
			t.Errorf("RunInfo.xml missing %q\n--- xml ---\n%s", want, xmlStr)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestRunInfo_OverrideFile(t *testing.T) {
	// When the user supplies a RunInfo.xml, it should be copied in verbatim.
	out := t.TempDir()
	overrideDir := t.TempDir()
	override := filepath.Join(overrideDir, "RunInfo.xml")
	if err := os.WriteFile(override, []byte(xml.Header+`<RunInfo Version="3"><Run Id="USER"/></RunInfo>`), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := DefaultSpec()
	spec.Out = out
	spec.RunInfoXMLPath = override

	if err := Generate(spec); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(out, "RunInfo.xml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var ri runInfo
	if err := xml.Unmarshal(got, &ri); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ri.Version != "3" || ri.Run.RunId != "USER" {
		t.Errorf("user-supplied RunInfo was not used: %s", got)
	}
}
