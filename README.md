# bclgen

[![CI](https://github.com/inful/bclgen/actions/workflows/test.yml/badge.svg)](https://github.com/inful/bclgen/actions/workflows/test.yml)
[![Release](https://img.shields.io/github/v/release/inful/bclgen.svg)](https://github.com/inful/bclgen/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/inful/bclgen)](https://goreportcard.com/report/github.com/inful/bclgen)

A small Go binary that generates a **mock Illumina BCL run folder** for
testing. The output matches what [`bcl2fastq` v2.20][guide] expects to read for
the HiSeq / MiSeq / HiSeq 2000/2500 family (the `*.bcl.gz` flavor — Table 1 in
the guide).

[guide]: https://support.illumina.com/content/dam/illumina-support/documents/documentation/software_documentation/bcl2fastq/bcl2fastq2-v2-20-software-guide-15051736-03.pdf

Use it to feed bcl2fastq (or any other BCL reader) test data without needing
a sequencer handy.

## What it writes

```
<out>/RunInfo.xml
<out>/Data/Intensities/BaseCalls/L<NNN>/
    s_<lane>_<tile>.locs
    s_<lane>_<tile>.filter
    C<cycle>.<surface>/s_<lane>_<tile>.bcl.gz   (one per cycle × surface)
[<out>/SampleSheet.csv]    (only if -sample-sheet is set)
```

A 4-digit tile id is `<surface><swath><tile-2digit>` per Illumina convention.
Surface is hardcoded to 1 for `.bcl.gz` (see `-surfaces`); lane is a
separate `s_<lane>_` prefix in the file name. Example: lane 1, swath 1,
tile 1 → file `s_1_1101.bcl.gz` (tile id 1101).

The on-disk byte layout follows the bcl2fastq v2.20 software guide:

- **BCL file** (Table 1): `gzip( uint32_LE(N) || N cluster bytes )`,
  where each cluster byte is `bits 0..1 = base [A=0, C=1, G=2, T=3]`,
  `bits 2..7 = Q-score << 2`, and `0x00` = no call.
- **Filter file** (Table 3): `uint32_LE(0) || uint32_LE(version=3) ||
  uint32_LE(N) || N bytes, bit 0 = pass-filter`.
- **Locs file**: 12-byte header `(uint32_LE 1, float32_LE 1.0,
  uint32_LE N)` followed by N records of `(float32_LE X, float32_LE Y)`.
- **RunInfo.xml**: one `<Read>`, `<FlowcellLayout>` with
  `LaneCount`/`SurfaceCount`/`SwathCount`/`TileCount`.

## Install

Pre-built binaries for Linux, macOS, and Windows (amd64 + arm64) are
attached to each [GitHub Release][releases]. Download one and put it on
your `$PATH`.

### Docker

Build the image locally (the project doesn't publish a prebuilt image —
see the disabled `dockers:` section in `.goreleaser.yml` for the
reason):

```sh
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bclgen ./cmd/bclgen
docker build -t bclgen:dev .
docker run --rm -v "$PWD:/out" bclgen:dev \
    -out /out/mockrun -cycles 26 -clusters 1000 -seed 1
```

The image is `gcr.io/distroless/static-debian12` plus the static Go
binary — about 10 MB total, no shell or package manager inside.

### From source

```sh
go install github.com/inful/bclgen/cmd/bclgen@latest
```

No third-party dependencies. Tested with Go 1.26.

[releases]: https://github.com/inful/bclgen/releases

## Usage

```sh
bclgen -out <runfolder> [flags]
```

The most useful flags are `-out` (required), `-cycles`, `-clusters`, and
`-seed`. Run `bclgen -help` for the full list.

### Examples

Minimal run folder, 1 lane, 26 cycles, 1000 clusters, deterministic:

```sh
bclgen -out /tmp/mockrun -cycles 26 -clusters 1000 -seed 1
```

Bigger dataset with two surfaces and partial pass-filter rate:

```sh
bclgen -out /tmp/mockrun -lanes 1 -swaths 2 -tiles 3 -surfaces 2 \
       -cycles 150 -clusters 5000 -pf-rate 0.8 -no-call-rate 0.001 -seed 42
```

Provide your own RunInfo.xml and sample sheet:

```sh
bclgen -out /tmp/mockrun -runinfo ./RunInfo.xml -sample-sheet ./SampleSheet.csv
```

Then point bcl2fastq at the folder:

```sh
bcl2fastq --runfolder-dir /tmp/mockrun --output-dir /tmp/fastq_out
```

If a run fails partway (disk full, quota, a permissions error), the
output directory is left partially written and every retry is refused.
Pass `-force` to replace it:

```sh
bclgen -out /tmp/mockrun -cycles 26 -clusters 1000 -seed 1 -force
```

## Flags

| Flag              | Default | Notes                                           |
|-------------------|---------|-------------------------------------------------|
| `-out`            | (required) | output run folder directory                  |
| `-lanes`          | 1       | lane count                                      |
| `-swaths`         | 1       | swaths per lane                                 |
| `-tiles`          | 1       | tiles per swath                                 |
| `-surfaces`       | 1       | 1 or 2; written into RunInfo.xml's `SurfaceCount`. bcl2fastq v2.20 hardcodes surface 1 in the cycle dir name for `.bcl.gz`, so only surface 1 files are emitted regardless. |
| `-cycles`         | 26      | total cycles; emitted as a single `<Read>` in RunInfo.xml |
| `-clusters`       | 1000    | clusters per tile                               |
| `-pf-rate`        | 1.0     | fraction of clusters that pass filter (0..1)    |
| `-no-call-rate`   | 0.0     | fraction of cluster bytes written as `0x00`     |
| `-qscore`         | 30      | Phred Q-score (0..63) packed into every cluster byte |
| `-seed`           | 1       | PRNG seed                                       |
| `-run-id`         | `run1`  | RunInfo `<RunId>`                               |
| `-instrument`     | `A00000` | RunInfo `<Instrument>`                         |
| `-date`           | today   | RunInfo `<Date>` in YYMMDD                      |
| `-flowcell`       | synthetic | RunInfo `<Flowcell>`                          |
| `-runinfo`        | (none)  | path to a hand-written RunInfo.xml to copy in; its lane/tile/cycle counts must match `-lanes`/`-swaths`/`-tiles`/`-cycles`, or the run is refused |
| `-sample-sheet`   | (none)  | path to a SampleSheet.csv to copy into the root |
| `-progress`       |         | print a progress line to stderr every ~2% of tiles (one line per tile under 50 tiles) |
| `-force`          |         | delete `-out` and everything under it first; needed to retry a run that failed partway |
| `-help`           |         | show help                                       |
| `-version`        |         | print the version and exit                      |

## Determinism

The same `-seed` and the same other flags produce **byte-identical** output
on every run — the PRNG drives base call, Q-score, X, Y, and PF bit for
every cluster. This is what makes golden-file and snapshot tests work.

Two seeding details are worth knowing:

- **Filter and locs files are per-tile; BCL files are per-(lane, cycle).**
  Filter and locs are seeded from `(lane, swath, tile)`, so every tile has its
  own pass-filter pattern and its own cluster coordinates. BCL files are
  seeded from `(lane, cycle)`, so all tiles of one `(lane, cycle)` share
  byte-identical bases. There is currently no way to give each tile different
  bases — `-sequence` overrides the bases for *every* tile equally, it does
  not differentiate them.
- **`-date` defaults to today.** A run's `RunInfo.xml` embeds the date, so
  byte-identical output across days requires passing `-date YYMMDD`.

## Tests

```sh
go test ./...
```

The unit + integration tests cover:

- cluster byte encoding / round-trip
- BCL.gz layout and round-trip
- filter file layout, PF rate, determinism
- locs file size and round-trip
- RunInfo.xml generation and override
- end-to-end `Generate` against the full layout
- validation errors
- `PFRate == 0` semantics ("no PF" is not silently bumped to 1.0)
- determinism of an entire run folder

bcl2fastq compatibility is verified by an end-to-end test that runs
the official `umccr/bcl2fastq:v2.20.0.422` Docker image against a
generated run folder. Requires Docker and downloads ~280 MB on first
run; build-tagged so it doesn't run in `go test ./...`:

```sh
go test -tags bcl2fastq_e2e ./internal/bclgen/
```

## Library use

The same code is exposed as a Go library:

```go
import "github.com/inful/bclgen/internal/bclgen"

err := bclgen.Generate(bclgen.RunSpec{
    Out:      "/tmp/mockrun",
    Cycles:   26,
    Clusters: 1000,
    Seed:     1,
})
```

Start from `bclgen.DefaultSpec()` to get the documented defaults and
override only the fields you care about.

## Limitations

- Only the **`.bcl.gz`** flavor is supported. The `.bcl.bgzf` (iSeq / MiniSeq
  / NextSeq) and `.cbcl` (NovaSeq 6000) flavors are not implemented yet.
- No `*.stats` files (MiSeq / HiSeq 2000/2500). The bcl2fastq guide lists
  these but they are optional and most modern instruments do not produce
  them.
- No `InterOp/` files (SAV-only, not consumed by bcl2fastq).
- All clusters are uniformly random bases at a single fixed Q-score. For
  edge-case test data (e.g. long homopolymers, low-quality tails), wire up
  your own `bclgen.RunSpec` from Go and call `Generate` directly.
