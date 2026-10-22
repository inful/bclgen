// Package bclgen: orchestration.
package bclgen

import (
	"fmt"
	"os"
	"path/filepath"
)

// Generate creates a complete run folder at spec.Out. The folder contains
// everything bcl2fastq v2.20 needs to produce FASTQ files for the
// HiSeq / MiSeq / HiSeq 2000/2500 instrument family:
//
//	RunInfo.xml
//	Data/Intensities/BaseCalls/L<NNN>/
//	    s_<lane>_<tile>.locs
//	    s_<lane>_<tile>.filter
//	    C<cycle>.<surface>/s_<lane>_<tile>.bcl.gz   (one per cycle × surface)
//	[SampleSheet.csv]   (only if spec.SampleSheetPath is set)
//
// The function is idempotent only in the sense that the same spec produces
// byte-identical output — it will refuse to write into a non-empty Out.
func Generate(spec RunSpec) error {
	spec = spec.fillDefaults()
	if err := spec.Validate(); err != nil {
		return err
	}
	if err := ensureEmptyDir(spec.Out); err != nil {
		return err
	}

	if err := writeRunInfo(spec); err != nil {
		return err
	}

	if spec.SampleSheetPath != "" {
		if err := copyFile(spec.SampleSheetPath, filepath.Join(spec.Out, "SampleSheet.csv")); err != nil {
			return err
		}
	}

	// Pre-create the directory tree once. Without this, every writeBCLFileToDisk
	// call would re-MkdirAll the same cycle directory, which is
	// O(Lanes * Swaths * Tiles * Cycles) syscalls for what only needs to be
	// O(Lanes + Lanes * Cycles).
	if err := precreateDirs(spec); err != nil {
		return err
	}

	totalTiles := spec.Lanes * spec.Swaths * spec.Tiles
	progressEvery := 0
	if spec.Progress {
		// Print at most ~50 evenly-spaced progress lines regardless of run size.
		// For tiny runs (≤50 tiles) we fall back to one line per tile.
		progressEvery = max(totalTiles/50, 1)
	}

	tilesDone := 0
	for lane := 1; lane <= spec.Lanes; lane++ {
		for swath := 1; swath <= spec.Swaths; swath++ {
			for tile := 1; tile <= spec.Tiles; tile++ {
				if err := writeLocsFile(spec, lane, swath, tile); err != nil {
					return err
				}
				if err := writeFilterFile(spec, lane, swath, tile); err != nil {
					return err
				}
				tileNum := spec.tileNumber(lane, swath, tile)
				for cycle := 1; cycle <= spec.Cycles; cycle++ {
					if err := writeBCLFileToDisk(spec, lane, cycle, tileNum); err != nil {
						return err
					}
				}
				tilesDone++
				if spec.Progress && (tilesDone == totalTiles || tilesDone%progressEvery == 0) {
					_, _ = fmt.Fprintf(os.Stderr, "bclgen: %d/%d tiles (%d BCL files)\n",
						tilesDone, totalTiles, tilesDone*spec.Cycles)
				}
			}
		}
	}
	return nil
}

// precreateDirs builds the per-lane and per-cycle directory tree once so
// the writers can assume their directories exist. Calling MkdirAll per
// writer invocation is O(Lanes * Swaths * Tiles * Cycles); this is
// O(Lanes + Lanes * Cycles). Each spec.cycleDir call creates the
// BaseCalls/L<NNN> parent as a side effect, so the only per-lane call we
// need is for the locs directory (which lives one level shallower).
func precreateDirs(spec RunSpec) error {
	for lane := 1; lane <= spec.Lanes; lane++ {
		if err := os.MkdirAll(spec.locsDir(lane), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", spec.locsDir(lane), err)
		}
		for cycle := 1; cycle <= spec.Cycles; cycle++ {
			dir := spec.cycleDir(lane, cycle)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", dir, err)
			}
		}
	}
	return nil
}

// ensureEmptyDir creates spec.Out if missing and errors if it already has
// contents. bclgen refuses to scribble over an existing run folder.
func ensureEmptyDir(path string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", path, err)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("readdir %s: %w", path, err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s is not empty (refusing to overwrite)", path)
	}
	return nil
}
