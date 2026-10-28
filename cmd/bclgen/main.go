// Command bclgen generates a mock Illumina BCL run folder suitable for
// feeding to bcl2fastq v2.20 (or to anything else that reads HiSeq / MiSeq
// *.bcl.gz inputs).
//
// Usage:
//
//	bclgen -out <runfolder> [flags]
//
// The most useful flags are -out (required), -cycles, -clusters, and -seed.
// Run bclgen -help for the full list.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/inful/bclgen/internal/bclgen"
)

// version is the build version. It is overridden at link time by goreleaser
// (-X main.version=...); "dev" is what a plain 'go build' produces.
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "bclgen: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("bclgen", flag.ContinueOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), `bclgen — generate a mock Illumina BCL run folder.

Usage:
  bclgen -out <dir> [flags]

Flags:
`)
		fs.PrintDefaults()
		_, _ = fmt.Fprintf(fs.Output(), `
Output layout (HiSeq / MiSeq *.bcl.gz convention):
  <out>/RunInfo.xml
  <out>/Data/Intensities/BaseCalls/L<NNN>/
      s_<lane>_<tile>.locs
      s_<lane>_<tile>.filter
      C<cycle>.<surface>/s_<lane>_<tile>.bcl.gz

Same -seed + same other flags ⇒ byte-identical output.

bclgen refuses to write into a non-empty -out. If a run failed partway
(disk full, quota, a permissions error), pass -force to replace the
partial folder and retry; everything under -out is deleted first.
`)
	}

	// Flag defaults come from DefaultSpec() so the CLI and library can
	// never disagree on a default. Date is the one field that stays
	// empty by flag default — its "default" is "today", which is
	// resolved at Generate time so two runs in the same invocation
	// always see the same date.
	defaults := bclgen.DefaultSpec()
	var (
		out         = fs.String("out", "", "output run folder directory (required)")
		lanes       = fs.Int("lanes", defaults.Lanes, "number of lanes")
		cycles      = fs.Int("cycles", defaults.Cycles, "number of cycles (single Read in RunInfo.xml)")
		swaths      = fs.Int("swaths", defaults.Swaths, "swaths per lane")
		tiles       = fs.Int("tiles", defaults.Tiles, "tiles per swath")
		clusters    = fs.Int("clusters", defaults.Clusters, "clusters per tile")
		surfaces    = fs.Int("surfaces", defaults.Surfaces, "surfaces per lane (1 or 2)")
		pfRate      = fs.Float64("pf-rate", defaults.PFRate, "fraction of clusters that pass filter (0..1)")
		noCallRate  = fs.Float64("no-call-rate", defaults.NoCallRate, "fraction of cluster bytes written as no-call (0..1)")
		qscore      = fs.Int("qscore", defaults.QScore, "Phred quality score written into every cluster byte (0..63)")
		seed        = fs.Uint64("seed", defaults.Seed, "PRNG seed; same seed + same flags => byte-identical output")
		runID       = fs.String("run-id", defaults.RunID, "RunInfo RunId")
		instrument  = fs.String("instrument", defaults.Instrument, "RunInfo Instrument")
		date        = fs.String("date", "", "RunInfo Date in YYMMDD (default: today)")
		flowcell    = fs.String("flowcell", defaults.Flowcell, "RunInfo Flowcell")
		runInfoPath = fs.String("runinfo", "", "path to a hand-written RunInfo.xml to copy in instead of generating one; its lane/tile/cycle counts must match -lanes/-swaths/-tiles/-cycles, or the run is refused")
		sampleSheet = fs.String("sample-sheet", "", "path to a SampleSheet.csv to copy into the run root")
		sequence    = fs.String("sequence", "", "per-cycle base pattern (e.g. \"ACGT\" or \"ACGTACGTN\"). When set, cycle i uses the i'th character (mod length) for ALL clusters, overriding random bases. Allowed: A/C/G/T/N (case-insensitive).")
		progress    = fs.Bool("progress", false, "print a progress line to stderr every ~2% of tiles (one line per tile for runs under 50 tiles)")
		force       = fs.Bool("force", false, "delete -out and everything under it before generating; needed to retry a run that failed partway")
		help        = fs.Bool("help", false, "show help")
		showVersion = fs.Bool("version", false, "print the version and exit")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Printf("bclgen %s\n", version)
		return nil
	}
	if *help {
		fs.Usage()
		return nil
	}
	if *out == "" {
		fs.Usage()
		return fmt.Errorf("missing required flag -out")
	}

	spec := bclgen.RunSpec{
		Out:             *out,
		Lanes:           *lanes,
		Swaths:          *swaths,
		Tiles:           *tiles,
		Surfaces:        *surfaces,
		Cycles:          *cycles,
		Clusters:        *clusters,
		PFRate:          *pfRate,
		NoCallRate:      *noCallRate,
		QScore:          *qscore,
		Seed:            *seed,
		RunID:           *runID,
		Instrument:      *instrument,
		Date:            *date,
		Flowcell:        *flowcell,
		RunInfoXMLPath:  *runInfoPath,
		SampleSheetPath: *sampleSheet,
		Sequence:        *sequence,
		Progress:        *progress,
		Force:           *force,
	}
	if err := bclgen.Generate(spec); err != nil {
		return err
	}
	fmt.Printf("wrote run folder to %s\n", *out)
	return nil
}
