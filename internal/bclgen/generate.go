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
	// Reject a -force target that would be catastrophic to delete before
	// touching the filesystem at all.
	if spec.Force {
		if err := checkForceSafe(spec.Out); err != nil {
			return err
		}
	}
	// Cross-check a hand-written RunInfo.xml before creating anything, so a
	// layout that disagrees with the spec fails without leaving a directory
	// behind. The validated bytes are carried through so the file that gets
	// written is exactly the one that was checked.
	override, err := checkRunInfoOverride(spec)
	if err != nil {
		return err
	}
	// Take an exclusive lock on the output directory before writing
	// anything. Two bclgen processes aimed at the same -out otherwise
	// interleave their writes and produce a folder that is a silent
	// mixture of both runs, with both processes reporting success.
	unlock, err := lockOutputDir(spec.Out)
	if err != nil {
		return err
	}
	defer unlock()
	if err := ensureOutputDir(spec); err != nil {
		return err
	}

	if err := writeRunInfo(spec, override); err != nil {
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

// ensureOutputDir prepares spec.Out for writing.
//
// By default bclgen refuses to scribble over a directory that already has
// contents in it. With spec.Force it removes whatever is there first, which is
// what makes a retry after a crashed run possible: a mid-run failure (disk
// full, quota, a permissions error on one cycle directory) leaves a partial
// folder behind, and without -force every retry fails with "is not empty"
// until the user removes the directory by hand.
//
// Force deletes spec.Out itself along with everything inside it, including
// files that have nothing to do with bclgen. It is opt-in for that reason.
// A filesystem root is refused outright: os.RemoveAll("/") cannot remove "/"
// itself (the mount point is busy) but it does remove every file inside it,
// so 'bclgen -out / -force' would wipe the disk.
func ensureOutputDir(spec RunSpec) error {
	if spec.Force {
		if err := os.RemoveAll(spec.Out); err != nil {
			return fmt.Errorf("remove %s: %w", spec.Out, err)
		}
	}
	if err := os.MkdirAll(spec.Out, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", spec.Out, err)
	}
	if spec.Force {
		return nil
	}
	entries, err := os.ReadDir(spec.Out)
	if err != nil {
		return fmt.Errorf("readdir %s: %w", spec.Out, err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s is not empty (refusing to overwrite; pass -force to replace it)", spec.Out)
	}
	return nil
}

// checkForceSafe refuses a -force target that would be catastrophic to
// delete. A filesystem root is the case that matters: os.RemoveAll cannot
// remove the mount point itself, but it does remove every file beneath it, so
// 'bclgen -out / -force' would empty the disk.
//
// A bare relative name like "." or ".." is refused too, because
// os.RemoveAll(".") fails with EINVAL but os.RemoveAll("..") would delete the
// parent directory.
func checkForceSafe(path string) error {
	clean := filepath.Clean(path)
	if clean == string(filepath.Separator) || clean == "." || clean == ".." {
		return fmt.Errorf("refusing to use -force on %q: that would delete everything above the output folder; -out must name a dedicated directory", path)
	}
	return nil
}

// lockOutputDir takes an exclusive lock on the output directory and returns
// the function that releases it.
//
// Without this, two bclgen processes aimed at the same -out interleave their
// writes: one process's ensureOutputDir sees an empty directory, the other's
// sees the same, and both go on to write files over each other. The result is
// a folder that is a mixture of the two runs, and both processes print
// "wrote run folder to ...". A silently-wrong output is worse than a
// refused one.
//
// The lock file lives beside the output directory, not inside it, so it never
// appears in the run folder bcl2fastq reads. It is created with O_EXCL, so
// the create itself is the atomic test-and-set.
//
// The lock is advisory: it stops two bclgen processes, not arbitrary writers.
// A stale lock file (left by a process killed with SIGKILL) blocks the next
// run until it is removed; the error says so.
func lockOutputDir(out string) (func(), error) {
	lockPath := out + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("%s is locked by another bclgen process; if that process is gone, remove %s and retry", out, lockPath)
		}
		return nil, fmt.Errorf("create lock %s: %w", lockPath, err)
	}
	_ = f.Close()
	return func() { _ = os.Remove(lockPath) }, nil
}
