package bclgen

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"path/filepath"
)

// Locs file path: <runfolder>/Data/Intensities/L<NNN>/s_<lane>_<%04d-tile>.locs.
//
// bcl2fastq v2.20 (src/cxx/lib/data/PositionsFile.cpp) computes intensitiesDir
// as --input-dir/../, which defaults to <runfolder>/Data/Intensities/.
// The per-tile .locs lookup is <intensitiesDir>/L<NNN>/s_<lane>_<tile>.locs.
//
// Locs file format (from PositionsFile.cpp, LocsFile::Header and Record):
//
//	Bytes 0..3   uint32 LE = field1, must equal 1
//	Bytes 4..7   float32 LE = field2, must equal 1.0
//	Bytes 8..11  uint32 LE = N (cluster count)
//	Bytes 12..   N records of { float32 X, float32 Y }  (8 bytes each)
//
// bcl2fastq transforms the X/Y floats to integer cluster coordinates as
//
//	x' = x * 10.0 + 1000.5
//	y' = y * 10.0 + 1000.5
//
// so the floats don't need to be in any particular range, but values around
// 0.0 are reasonable.
//
// The v2.20 software guide describes a simpler "binary file of cluster
// positions" without the magic header — that description is for the older
// s.locs format and doesn't match what the source actually reads.
func writeLocsFile(spec RunSpec, lane, swath, tile int) error {
	// Directory is pre-created by Generate.precreateDirs.
	dir := spec.locsDir(lane)
	tileNum := spec.tileNumber(lane, swath, tile)
	path := filepath.Join(dir, fmt.Sprintf("s_%d_%04d.locs", lane, tileNum))
	return writeBinary(path, func(w io.Writer) error {
		return writeLocs(w, spec)
	})
}

// writeLocs writes the per-tile locs payload to w: a 12-byte header
// (field1=1, field2=1.0, N) followed by N records of { float32 X, float32 Y }.
// RNG calls happen in ascending cluster order, preserving determinism.
func writeLocs(w io.Writer, spec RunSpec) error {
	var hdr [12]byte
	binary.LittleEndian.PutUint32(hdr[0:4], 1)                     // field1 = 1
	binary.LittleEndian.PutUint32(hdr[4:8], math.Float32bits(1.0)) // field2 = 1.0
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(spec.Clusters))
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("write locs header: %w", err)
	}

	// 8 bytes per cluster (float32 X, float32 Y). Stream in 512-record
	// (4096-byte) chunks so memory is bounded for large Clusters.
	const chunkRecords = 512
	chunk := make([]locsRecord, chunkRecords)
	rng := spec.rng()
	remaining := spec.Clusters
	for remaining > 0 {
		n := min(remaining, len(chunk))
		for i := range n {
			// X and Y around 0.0 (any value works; bcl2fastq transforms to int).
			chunk[i].X = float32(rng.Float64()*1000.0) / 10.0
			chunk[i].Y = float32(rng.Float64()*1000.0) / 10.0
		}
		if err := binary.Write(w, binary.LittleEndian, chunk[:n]); err != nil {
			return fmt.Errorf("write locs body: %w", err)
		}
		remaining -= n
	}
	return nil
}

// locsRecord is the on-disk record shape: two little-endian float32 values.
// binary.Write serializes the fields without padding, giving 8 bytes per
// record — identical to the previous hand-rolled PutUint32 layout.
type locsRecord struct {
	X, Y float32
}
