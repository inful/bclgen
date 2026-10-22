package bclgen

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// runInfo mirrors the RunInfo.xml schema that bcl2fastq v2.20 reads.
//
// Field placement is taken from the bcl2fastq source
// (src/cxx/lib/layout/RunInfoXml.cpp): FlowcellLayout's lane/surface/swath/tile
// counts must be XML attributes, and the Run id is an XML attribute on <Run>.
// <Instrument>, <Date>, <Flowcell> are still child elements.
type runInfo struct {
	XMLName xml.Name   `xml:"RunInfo"`
	Version string     `xml:"Version,attr"`
	Run     runInfoRun `xml:"Run"`
}

type runInfoRun struct {
	RunID      string        `xml:"Id,attr"`     // bcl2fastq reads RunInfo.Run.<xmlattr>.Id
	Number     int           `xml:"Number,attr"` // bcl2fastq reads RunInfo.Run.<xmlattr>.Number (optional)
	Instrument string        `xml:"Instrument"`
	Date       string        `xml:"Date"`
	Flowcell   string        `xml:"Flowcell"`
	Reads      runInfoReads  `xml:"Reads"`
	Layout     runInfoLayout `xml:"FlowcellLayout"`
}

type runInfoReads struct {
	Reads []runInfoRead `xml:"Read"`
}

type runInfoRead struct {
	Number    int    `xml:"Number,attr"`
	NumCycles int    `xml:"NumCycles,attr"`
	IsIndexed string `xml:"IsIndexedRead,attr"`
}

// FlowcellLayout is encoded as a self-closing element with all four required
// attributes on the element. bcl2fastq reads them as <xmlattr> on FlowcellLayout.
type runInfoLayout struct {
	LaneCount    int `xml:"LaneCount,attr"`
	SurfaceCount int `xml:"SurfaceCount,attr"`
	SwathCount   int `xml:"SwathCount,attr"`
	TileCount    int `xml:"TileCount,attr"`
}

// writeRunInfo emits <Out>/RunInfo.xml describing the run.
//
// If spec.RunInfoXMLPath is set, that file is used instead of generating one —
// useful when the user wants full control over read structure. Generate has
// already cross-checked it against the spec (checkRunInfoOverride), before
// creating any directories.
func writeRunInfo(spec RunSpec) error {
	if spec.RunInfoXMLPath != "" {
		return copyFile(spec.RunInfoXMLPath, filepath.Join(spec.Out, "RunInfo.xml"))
	}

	ri := runInfo{
		Version: "2",
		Run: runInfoRun{
			RunID:      spec.RunID,
			Number:     1,
			Instrument: spec.Instrument,
			Date:       spec.Date,
			Flowcell:   spec.Flowcell,
			Reads: runInfoReads{
				Reads: []runInfoRead{{
					Number:    1,
					NumCycles: spec.Cycles,
					IsIndexed: "N",
				}},
			},
			Layout: runInfoLayout{
				LaneCount:    spec.Lanes,
				SurfaceCount: spec.Surfaces,
				SwathCount:   spec.Swaths,
				TileCount:    spec.Tiles,
			},
		},
	}

	body, err := xml.MarshalIndent(ri, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal RunInfo.xml: %w", err)
	}
	// bcl2fastq expects the XML declaration on the first line.
	body = append([]byte(xml.Header), body...)

	// spec.Out was already MkdirAll'd by Generate.ensureEmptyDir.
	path := filepath.Join(spec.Out, "RunInfo.xml")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// checkRunInfoOverride parses the user-supplied RunInfo.xml and verifies that
// the lane/swath/tile/cycle counts it declares match what Generate is about to
// write. Without this a hand-written RunInfo claiming 8 lanes next to a spec
// of 1 lane produces a folder bcl2fastq cannot read, and bclgen reports
// success.
//
// A malformed or unexpected RunInfo is only warned about, never rejected: the
// point of -runinfo is full manual control, and refusing to run would defeat
// it. Only a clear disagreement between the declared layout and the generated
// data is an error.
func checkRunInfoOverride(spec RunSpec) error {
	if spec.RunInfoXMLPath == "" {
		return nil // generated RunInfo always matches by construction
	}
	body, err := os.ReadFile(spec.RunInfoXMLPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", spec.RunInfoXMLPath, err)
	}
	var ri runInfo
	if err := xml.Unmarshal(body, &ri); err != nil {
		// Not parseable as RunInfo — the user is on their own.
		fmt.Fprintf(os.Stderr, "bclgen: warning: %s is not parseable as RunInfo.xml (%v); copying it verbatim\n",
			spec.RunInfoXMLPath, err)
		return nil
	}

	var mismatches []string
	want := map[string][2]int{
		"LaneCount":    {ri.Run.Layout.LaneCount, spec.Lanes},
		"SurfaceCount": {ri.Run.Layout.SurfaceCount, spec.Surfaces},
		"SwathCount":   {ri.Run.Layout.SwathCount, spec.Swaths},
		"TileCount":    {ri.Run.Layout.TileCount, spec.Tiles},
	}
	for _, name := range []string{"LaneCount", "SurfaceCount", "SwathCount", "TileCount"} {
		if got, exp := want[name][0], want[name][1]; got != exp {
			mismatches = append(mismatches, fmt.Sprintf("%s: RunInfo says %d, spec says %d", name, got, exp))
		}
	}
	// bclgen only ever emits a single <Read>, so require the override to
	// declare exactly one read with the spec's cycle count.
	switch reads := ri.Run.Reads.Reads; len(reads) {
	case 0:
		mismatches = append(mismatches, "RunInfo declares no <Read> but bclgen generates 1")
	case 1:
		if reads[0].NumCycles != spec.Cycles {
			mismatches = append(mismatches,
				fmt.Sprintf("NumCycles: RunInfo says %d, spec says %d", reads[0].NumCycles, spec.Cycles))
		}
	default:
		mismatches = append(mismatches,
			fmt.Sprintf("Read count: RunInfo declares %d reads but bclgen generates 1", len(reads)))
	}

	if len(mismatches) > 0 {
		return fmt.Errorf("%s does not match the spec: %s (bcl2fastq reads the layout from RunInfo.xml, so the generated data would not match it)",
			spec.RunInfoXMLPath, strings.Join(mismatches, "; "))
	}
	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(dst), err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}
