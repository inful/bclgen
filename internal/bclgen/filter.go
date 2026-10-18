package bclgen

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Filter file header version emitted by bclgen. bcl2fastq v2.20
// (src/cxx/lib/data/FilterFile.cpp) requires header_version=3.
//
// Layout (from bcl2fastq source):
//
//	Bytes 0..3   uint32 LE = 0 (marker: "real header follows")
//	Bytes 4..7   uint32 LE = version (must equal 3)
//	Bytes 8..11  uint32 LE = N (cluster count)
//	Bytes 12..   N bytes, bit 0 = pass-filter
//
// bcl2fastq's FilterFile::readHeader reads the first 4 bytes as a uint32
// cluster count. If 0, it then reads the next 8 bytes as the (version,
// N) header. If non-zero, it treats the first 4 bytes AS the cluster
// count and the file has no version field. bclgen always emits the
// 12-byte header form, which matches the v2.20 software guide (Table 3)
// and BCL Convert.
const filterFileVersion uint32 = 3

// writeFilterFile writes s_<lane>_<tile>.filter for the given lane+swath+tile.
//
// The path follows bcl2fastq's filter file lookup (FilterFile.cpp):
// <InputDir>/L<NNN>/s_<lane>_<%04d-tile>.filter, where InputDir defaults to
// <runfolder>/Data/Intensities/BaseCalls/.
func writeFilterFile(spec RunSpec, lane, swath, tile int) error {
	// Directory is pre-created by Generate.precreateDirs.
	dir := spec.laneDir(lane)
	tileNum := spec.tileNumber(lane, swath, tile)
	path := fmt.Sprintf("%s/s_%d_%04d.filter", dir, lane, tileNum)
	return writeBinary(path, func(w io.Writer) error {
		return writeFilter(w, spec)
	})
}

// writeFilter writes the per-tile filter payload to w. Header (12 bytes:
// marker=0, version=3, N) followed by N bytes with bit 0 = pass-filter.
// At PFRate >= 1.0 the RNG is skipped entirely. The body streams in 4 KiB
// chunks (matching BCL and locs) so memory use stays bounded for large
// Clusters.
func writeFilter(w io.Writer, spec RunSpec) error {
	var hdr [12]byte
	binary.LittleEndian.PutUint32(hdr[0:4], 0)                 // marker: header follows
	binary.LittleEndian.PutUint32(hdr[4:8], filterFileVersion) // version = 3
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(spec.Clusters))
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("bclgen: write filter header: %w", err)
	}

	if spec.PFRate >= 1.0 {
		// Fast path: every cluster passes filter; skip the RNG entirely.
		// Fill the chunk with 0x01 and stream it.
		return streamChunks(w, spec.Clusters, func(buf []byte) error {
			for i := range buf {
				buf[i] = 0x01
			}
			return nil
		})
	}
	rng := spec.rng()
	return streamChunks(w, spec.Clusters, func(buf []byte) error {
		for i := range buf {
			if rng.Float64() < spec.PFRate {
				buf[i] = 0x01
			}
		}
		return nil
	})
}
