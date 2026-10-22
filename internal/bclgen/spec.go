// Package bclgen generates mock Illumina BCL run folders for testing.
//
// The output matches the layout bcl2fastq v2.20 expects for the HiSeq / MiSeq
// family (Table 1: BCL, Table 3: filter, plus RunInfo.xml and .locs).
// See https://support.illumina.com/content/dam/illumina-support/documents/documentation/software_documentation/bcl2fastq/bcl2fastq2-v2-20-software-guide-15051736-03.pdf
package bclgen

import (
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"time"
)

// RunSpec is the full configuration for a generated run folder.
//
// Zero values for Int fields fall back to the defaults returned by DefaultSpec;
// zero values for Float fields fall back to DefaultSpec as well. Callers should
// start from DefaultSpec and override only the fields they care about.
type RunSpec struct {
	// Out is the root directory of the run folder to create. Required.
	Out string

	// Lane count (1..N). Default 1.
	Lanes int
	// Swaths per lane. Default 1.
	Swaths int
	// Tiles per swath. Default 1.
	Tiles int
	// Surfaces per lane (1 or 2 in real instruments). Default 1.
	Surfaces int
	// Cycles. Treated as a single Read in RunInfo.xml. Default 26.
	Cycles int
	// Clusters per tile. Default 1000.
	Clusters int

	// PFRate is the fraction of clusters that pass filter, in [0, 1]. Default 1.0.
	PFRate float64
	// NoCallRate is the fraction of cluster bytes emitted as 0x00 (no call), in [0, 1]. Default 0.0.
	NoCallRate float64
	// QScore is the Phred quality score written into every cluster byte. Default 30.
	// Encoded into bits 2..7 of each cluster byte (Q << 2). Range 0..63.
	QScore int

	// Seed drives every per-cluster decision (base, Q, X, Y, PF). Default 1.
	// Same seed + same other knobs → byte-identical output.
	Seed uint64

	// Optional: per-cycle base pattern (e.g. "ACGT" or "ACGTACGTN"). When
	// non-empty, cycle i uses the i'th character of Sequence (mod length)
	// for ALL clusters in that cycle, overriding the random per-cluster
	// base. This is useful for regression testing against bcl2fastq: you
	// can verify that the FASTQ output matches a known input pattern.
	//
	// Allowed characters: A, C, G, T, N (case-insensitive). 'N' is emitted
	// as a no-call (cluster byte = 0x00). Other characters are rejected.
	Sequence string

	// RunInfo metadata. Empty strings fall back to sensible defaults.
	RunID      string
	Instrument string
	Date       string // YYMMDD; defaults to today's date.
	Flowcell   string

	// Optional: if non-empty, copy this RunInfo.xml into the run root instead
	// of generating one. RunSpec.Lanes/Swaths/Tiles/Surfaces/Cycles are still
	// used to size the data files.
	RunInfoXMLPath string

	// Optional: if non-empty, copy this file to <Out>/SampleSheet.csv.
	SampleSheetPath string

	// Progress, if true, prints a one-line progress update to stderr
	// every progressStep tiles. Default false.
	Progress bool
}

// DefaultSpec returns a RunSpec that produces a tiny but valid run folder.
func DefaultSpec() RunSpec {
	return RunSpec{
		Lanes:      1,
		Swaths:     1,
		Tiles:      1,
		Surfaces:   1,
		Cycles:     26,
		Clusters:   1000,
		PFRate:     1.0,
		NoCallRate: 0.0,
		QScore:     30,
		Seed:       1,
		RunID:      "run1",
		Instrument: "A00000",
		Date:       time.Now().Format("060102"),
		Flowcell:   "000000000-000000000",
	}
}

// fillDefaults replaces zero-valued fields with their defaults. Returns a copy.
//
// Float fields (PFRate, NoCallRate) are intentionally NOT reset to defaults
// when zero: 0 is a valid, useful value ("no filter" / "no no-calls") and
// differs from "unset". Library users get the documented default by
// starting from DefaultSpec(); Generate calls fillDefaults so a hand-built
// RunSpec with missing fields still works.
func (s RunSpec) fillDefaults() RunSpec {
	d := DefaultSpec()
	if s.Lanes == 0 {
		s.Lanes = d.Lanes
	}
	if s.Swaths == 0 {
		s.Swaths = d.Swaths
	}
	if s.Tiles == 0 {
		s.Tiles = d.Tiles
	}
	if s.Surfaces == 0 {
		s.Surfaces = d.Surfaces
	}
	if s.Cycles == 0 {
		s.Cycles = d.Cycles
	}
	if s.Clusters == 0 {
		s.Clusters = d.Clusters
	}
	if s.QScore == 0 {
		s.QScore = d.QScore
	}
	if s.Seed == 0 {
		s.Seed = d.Seed
	}
	if s.RunID == "" {
		s.RunID = d.RunID
	}
	if s.Instrument == "" {
		s.Instrument = d.Instrument
	}
	if s.Date == "" {
		s.Date = d.Date
	}
	if s.Flowcell == "" {
		s.Flowcell = d.Flowcell
	}
	return s
}

// Validate checks the spec for out-of-range values. Returns the first error.
//
// Upper bounds on Lanes/Swaths/Tiles protect the on-disk file-name format
// (L%03d for lane, %04d for tile = surface*1000 + swath*100 + tile). Values
// that overflow the format produce file names bcl2fastq v2.20 won't match.
func (s RunSpec) Validate() error {
	if s.Out == "" {
		return fmt.Errorf("spec.Out is required")
	}
	if s.Lanes < 1 || s.Lanes > 999 {
		return fmt.Errorf("spec.Lanes must be in [1, 999], got %d", s.Lanes)
	}
	if s.Surfaces != 1 && s.Surfaces != 2 {
		return fmt.Errorf("spec.Surfaces must be 1 or 2, got %d", s.Surfaces)
	}
	if s.Swaths < 1 || s.Swaths > 9 {
		return fmt.Errorf("spec.Swaths must be in [1, 9], got %d", s.Swaths)
	}
	if s.Tiles < 1 || s.Tiles > 99 {
		return fmt.Errorf("spec.Tiles must be in [1, 99], got %d", s.Tiles)
	}
	if s.Cycles < 1 {
		return fmt.Errorf("spec.Cycles must be >= 1, got %d", s.Cycles)
	}
	if s.Clusters < 1 {
		return fmt.Errorf("spec.Clusters must be >= 1, got %d", s.Clusters)
	}
	// The per-tile cluster count is written as a uint32 in every file
	// header (BCL Table 1, filter Table 3, locs PositionsFile.cpp), so
	// anything above 0xFFFFFFFF cannot be represented. Checked here
	// rather than left to writeBCLStream because locs and filter are
	// written before it and would emit Clusters*8 and Clusters*1
	// bytes of unrepresentable data first.
	if s.Clusters > 0xFFFFFFFF {
		return fmt.Errorf("spec.Clusters must be <= %d (32-bit file header), got %d", int64(0xFFFFFFFF), s.Clusters)
	}
	if s.PFRate < 0 || s.PFRate > 1 {
		return fmt.Errorf("spec.PFRate must be in [0,1], got %v", s.PFRate)
	}
	if s.NoCallRate < 0 || s.NoCallRate > 1 {
		return fmt.Errorf("NoCallRate must be in [0,1], got %v", s.NoCallRate)
	}
	if s.QScore < 0 || s.QScore > 63 {
		return fmt.Errorf("QScore must be in [0,63], got %d", s.QScore)
	}
	if s.Sequence != "" {
		for i := range len(s.Sequence) {
			if _, _, ok := parseBase(s.Sequence[i]); !ok {
				return fmt.Errorf("invalid base %q in sequence %q (allowed: A/C/G/T/N)", s.Sequence[i], s.Sequence)
			}
		}
	}
	return nil
}

// tileRng constructs an RNG whose state varies by lane, swath and tile. Two
// calls with different (lane, swath, tile) produce different sequences; two
// calls with the same triple and the same spec.Seed produce the same sequence.
//
// Used for the once-per-tile files (filter, locs) so each tile gets its own
// pass-filter pattern and its own cluster coordinates. Seeding these from
// spec.Seed alone — as an earlier revision did — made every .filter and .locs
// file in a run byte-identical, which -swaths and -tiles then only multiplied
// by file name.
func (s RunSpec) tileRng(lane, swath, tile int) *rand.Rand {
	// Mix lane, swath and tile into both PCG seed halves. Constants from
	// SplitMix64 / xxhash — the exact values don't matter, only that they
	// are large odd numbers with good avalanche behavior.
	l, w, t := uint64(lane), uint64(swath), uint64(tile)
	seed1 := s.Seed ^ l*0x9E3779B97F4A7C15 ^ w*0xBF58476D1CE4E5B9 ^ t*0x94D049BB133111EB
	seed2 := s.Seed*0xC6BC279692B5C323 ^ l*0x52DCE729 ^ w*0xD6E8FEB86659FD93 ^ t*0x2545F4914F6CDD1D
	return rand.New(rand.NewPCG(seed1, seed2))
}

// cycleRng constructs an RNG whose state varies by lane and cycle. Two calls
// with different cycles produce different sequences; two calls with the same
// (lane, cycle) and the same spec.Seed produce the same sequence.
//
// Used for BCL files so that the cycles in a run don't all come out
// byte-identical. Like tileRng it does not vary by tile, so every tile of a
// given (lane, cycle) shares a byte-identical BCL — see generate.go.
func (s RunSpec) cycleRng(lane, cycle int) *rand.Rand {
	// Mix lane and cycle into both PCG seed halves. Constants from
	// SplitMix64 / xxhash — the exact values don't matter, only that they
	// are large odd numbers with good avalanche behavior.
	seed1 := s.Seed ^ uint64(cycle)*0x9E3779B97F4A7C15 ^ uint64(lane)*0xBF58476D1CE4E5B9
	seed2 := s.Seed*0x94D049BB133111EB ^ uint64(cycle)*0xC6BC279692B5C323 ^ uint64(lane)*0x52DCE729
	return rand.New(rand.NewPCG(seed1, seed2))
}

// laneDir returns the per-lane directory inside the BaseCalls tree.
func (s RunSpec) laneDir(lane int) string {
	return filepath.Join(s.Out, "Data", "Intensities", "BaseCalls", fmt.Sprintf("L%03d", lane))
}

// locsDir returns the per-lane directory for locs files. bcl2fastq
// (PositionsFile.cpp) reads positions from <runfolder>/Data/Intensities/
// (one level shallower than BaseCalls/).
func (s RunSpec) locsDir(lane int) string {
	return filepath.Join(s.Out, "Data", "Intensities", fmt.Sprintf("L%03d", lane))
}

// cycleDir returns the per-cycle directory: C<cycle>.1.
//
// The bcl2fastq v2.20 source (data/BclFile.cpp) hardcodes the surface to 1 in
// the cycle directory for the .bcl.gz flavor, so we always emit surface 1
// regardless of spec.Surfaces. spec.Surfaces still goes into RunInfo.xml as
// the flow cell's physical surface count.
func (s RunSpec) cycleDir(lane, cycle int) string {
	return filepath.Join(s.laneDir(lane), fmt.Sprintf("C%d.1", cycle))
}

// tileNumber returns the tile id used in file names. bcl2fastq computes
// this as (tile + swath*100 + surface*1000) in RunInfoXml.cpp; for the
// .bcl.gz flavor, surface is always 1 in file lookups, so the thousands
// digit of the tile number is the surface (1 for .bcl.gz). Lane is NOT
// encoded in the tile number — lane is a separate path component.
//
// For lane 1, swath 1, tile 1: 1101.
func (s RunSpec) tileNumber(lane, swath, tile int) int {
	_ = lane // lane is intentionally not part of the tile number
	return 1*1000 + swath*100 + tile
}
