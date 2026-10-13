package bclgen

import (
	"encoding/binary"
	"fmt"
	"os"
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
	rng := spec.rng()

	var hdr [12]byte
	binary.LittleEndian.PutUint32(hdr[0:4], 0)                 // marker: header follows
	binary.LittleEndian.PutUint32(hdr[4:8], filterFileVersion) // version = 3
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(spec.Clusters))

	// Directory is pre-created by Generate.precreateDirs.
	dir := spec.laneDir(lane)
	tileNum := spec.tileNumber(lane, swath, tile)
	path := fmt.Sprintf("%s/s_%d_%04d.filter", dir, lane, tileNum)
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("bclgen: create %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Write(hdr[:]); err != nil {
		return fmt.Errorf("bclgen: write filter header: %w", err)
	}
	pf := make([]byte, spec.Clusters)
	if spec.PFRate >= 1.0 {
		// Fast path: every cluster passes filter.
		for i := range pf {
			pf[i] = 0x01
		}
	} else {
		for i := range pf {
			if rng.Float64() < spec.PFRate {
				pf[i] = 0x01
			}
		}
	}
	if _, err := f.Write(pf); err != nil {
		return fmt.Errorf("bclgen: write filter bytes: %w", err)
	}
	return nil
}
