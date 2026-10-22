package bclgen

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
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
	if ri.Run.RunID != "abc" {
		t.Errorf("RunId = %q, want abc", ri.Run.RunID)
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
	// When the user supplies a RunInfo.xml whose layout matches the spec, it
	// is copied in verbatim.
	out := t.TempDir()
	overrideDir := t.TempDir()
	override := filepath.Join(overrideDir, "RunInfo.xml")
	body := xml.Header + `<RunInfo Version="3"><Run Id="USER">` +
		`<Reads><Read Number="1" NumCycles="26" IsIndexedRead="N"/></Reads>` +
		`<FlowcellLayout LaneCount="1" SurfaceCount="1" SwathCount="1" TileCount="1"/>` +
		`</Run></RunInfo>`
	if err := os.WriteFile(override, []byte(body), 0o644); err != nil {
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
	if ri.Version != "3" || ri.Run.RunID != "USER" {
		t.Errorf("user-supplied RunInfo was not used: %s", got)
	}
}

// TestRunInfo_OverrideMismatchRejected pins the cross-check: bcl2fastq reads
// the lane/swath/tile/cycle counts out of RunInfo.xml and iterates them, so a
// hand-written RunInfo that disagrees with the spec describes data bclgen
// never writes. Generate must refuse rather than report success.
func TestRunInfo_OverrideMismatchRejected(t *testing.T) {
	cases := []struct {
		name     string
		xml      string
		wantWord string
	}{
		{
			name: "lane count",
			xml: `<RunInfo Version="2"><Run Id="U">` +
				`<Reads><Read Number="1" NumCycles="26"/></Reads>` +
				`<FlowcellLayout LaneCount="8" SurfaceCount="1" SwathCount="1" TileCount="1"/>` +
				`</Run></RunInfo>`,
			wantWord: "LaneCount",
		},
		{
			name: "tile count",
			xml: `<RunInfo Version="2"><Run Id="U">` +
				`<Reads><Read Number="1" NumCycles="26"/></Reads>` +
				`<FlowcellLayout LaneCount="1" SurfaceCount="1" SwathCount="1" TileCount="16"/>` +
				`</Run></RunInfo>`,
			wantWord: "TileCount",
		},
		{
			name: "cycle count",
			xml: `<RunInfo Version="2"><Run Id="U">` +
				`<Reads><Read Number="1" NumCycles="150"/></Reads>` +
				`<FlowcellLayout LaneCount="1" SurfaceCount="1" SwathCount="1" TileCount="1"/>` +
				`</Run></RunInfo>`,
			wantWord: "NumCycles",
		},
		{
			name: "multiple reads",
			xml: `<RunInfo Version="2"><Run Id="U">` +
				`<Reads><Read Number="1" NumCycles="26"/><Read Number="2" NumCycles="8"/></Reads>` +
				`<FlowcellLayout LaneCount="1" SurfaceCount="1" SwathCount="1" TileCount="1"/>` +
				`</Run></RunInfo>`,
			wantWord: "Read count",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			override := filepath.Join(t.TempDir(), "RunInfo.xml")
			if err := os.WriteFile(override, []byte(xml.Header+c.xml), 0o644); err != nil {
				t.Fatal(err)
			}
			spec := DefaultSpec()
			spec.Out = filepath.Join(t.TempDir(), "run")
			spec.RunInfoXMLPath = override

			err := Generate(spec)
			if err == nil {
				t.Fatalf("Generate accepted a RunInfo whose %s disagrees with the spec", c.wantWord)
			}
			if !strings.Contains(err.Error(), c.wantWord) {
				t.Errorf("error should name %s, got: %v", c.wantWord, err)
			}
			// Nothing should have been written.
			if _, statErr := os.Stat(spec.Out); statErr == nil {
				t.Errorf("output directory was created despite the mismatch")
			}
		})
	}
}

// TestRunInfo_OverrideUnparseableIsCopied documents that -runinfo still means
// full manual control: a file bclgen cannot parse as RunInfo.xml is warned
// about and copied verbatim rather than rejected.
func TestRunInfo_OverrideUnparseableIsCopied(t *testing.T) {
	out := t.TempDir()
	override := filepath.Join(t.TempDir(), "RunInfo.xml")
	const raw = `this is not xml at all`
	if err := os.WriteFile(override, []byte(raw), 0o644); err != nil {
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
	if string(got) != raw {
		t.Errorf("unparseable RunInfo was not copied verbatim: %q", got)
	}
}
