package bclgen

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
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
// If spec.RunInfoXMLPath is set, that file is copied in instead of generating
// one — useful when the user wants full control over read structure.
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
		return fmt.Errorf("bclgen: marshal RunInfo.xml: %w", err)
	}
	// bcl2fastq expects the XML declaration on the first line.
	body = append([]byte(xml.Header), body...)

	// spec.Out was already MkdirAll'd by Generate.ensureEmptyDir.
	path := filepath.Join(spec.Out, "RunInfo.xml")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("bclgen: write %s: %w", path, err)
	}
	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("bclgen: read %s: %w", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("bclgen: mkdir %s: %w", filepath.Dir(dst), err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return fmt.Errorf("bclgen: write %s: %w", dst, err)
	}
	return nil
}
