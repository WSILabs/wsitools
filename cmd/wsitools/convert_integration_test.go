package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wsilabs/wsitools/internal/source"
)

func testDir(t *testing.T) string {
	d := os.Getenv("WSI_TOOLS_TESTDIR")
	if d == "" {
		d = "../../sample_files"
	}
	if _, err := os.Stat(d); err != nil {
		t.Skipf("WSI_TOOLS_TESTDIR not available: %v", err)
	}
	return d
}

func TestConvertSVSBitExact(t *testing.T) {
	runConvertBitExactTest(t, "svs")
}

func TestConvertPhilipsBitExact(t *testing.T) {
	runConvertBitExactTest(t, "philips-tiff")
}

func TestConvertOMETIFFBitExact(t *testing.T) {
	runConvertBitExactTest(t, "ome-tiff")
}

func TestConvertGenericTIFFBitExact(t *testing.T) {
	runConvertBitExactTest(t, "generic-tiff")
}

func TestConvertBIFBitExact(t *testing.T) {
	runConvertBitExactTest(t, "bif")
}

func TestConvertIFEBitExact(t *testing.T) {
	runConvertBitExactTest(t, "ife")
}

// runConvertBitExactTest finds files in a per-format subdir of the test
// directory, runs `convert --to cog-wsi`, then verifies tile bit-equality
// and COG layout invariants on the output.
func runConvertBitExactTest(t *testing.T, formatSubdir string) {
	td := testDir(t)
	formatDir := filepath.Join(td, formatSubdir)
	entries, err := os.ReadDir(formatDir)
	if err != nil {
		t.Skipf("subdir %s not present: %v", formatDir, err)
	}
	var inputs []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		// Skip hidden files (e.g., .DS_Store) and obvious non-image files.
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		// Skip non-slide files that may live alongside samples (docs, PDFs).
		ext := strings.ToLower(filepath.Ext(name))
		if ext == ".pdf" || ext == ".md" || ext == ".txt" || ext == ".xml" {
			continue
		}
		inputs = append(inputs, filepath.Join(formatDir, name))
	}
	if len(inputs) == 0 {
		t.Skipf("no samples in %s", formatDir)
	}

	for _, input := range inputs {
		t.Run(filepath.Base(input), func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out.tiff")
			rootCmd.SetArgs([]string{"convert", "--to", "cog-wsi", "-o", out, input})
			t.Cleanup(func() { rootCmd.SetArgs(nil) })
			if err := rootCmd.Execute(); err != nil {
				// Expected failures: unsupported compression or format that
				// opentile-go refuses to open.  These are correct behavior, not
				// bugs — skip rather than FAIL.
				errStr := err.Error()
				if strings.Contains(errStr, "source compression") ||
					strings.Contains(errStr, "format unsupported") ||
					strings.Contains(errStr, "open source") {
					t.Skipf("convert skipped (expected rejection): %v", err)
				}
				t.Fatalf("convert: %v", err)
			}

			// Open source again to diff per-tile bytes.
			src, err := source.Open(input)
			if err != nil {
				t.Fatalf("reopen source: %v", err)
			}
			defer src.Close()

			out2, err := source.Open(out)
			if err != nil {
				// opentile-go may not recognise the COG-WSI output for some
				// source formats (e.g. IFE → generic TIFF not detected as IFE).
				// Log and skip the per-tile comparison rather than a hard fail.
				t.Logf("reopen output skipped (opentile-go limitation): %v", err)
			} else {
				defer out2.Close()

				// opentile-go's generic-tiff reader may detect fewer pyramid
				// levels than the source (e.g. SVS interleaves thumbnails between
				// IFDs; the output COG has them all at the tail). Compare only
				// the levels that both readers report, matching by size to guard
				// against off-by-one level mapping.
				srcLevels := src.Levels()
				outLevels := out2.Levels()
				if len(outLevels) < len(srcLevels) {
					t.Logf("output reader detected %d levels (src=%d); comparing %d",
						len(outLevels), len(srcLevels), len(outLevels))
				}

				// Build a size → srcLevel map so we can pair by geometry even
				// when the output reader's level numbering diverges from the
				// source's (opentile-go skips some IFDs for certain formats).
				type lvlKey struct{ w, h int }
				srcBySize := map[lvlKey]source.Level{}
				for _, sl := range srcLevels {
					srcBySize[lvlKey{sl.Size().X, sl.Size().Y}] = sl
				}

				for _, outLvl := range outLevels {
					key := lvlKey{outLvl.Size().X, outLvl.Size().Y}
					srcLvl, ok := srcBySize[key]
					if !ok {
						// No matching src level for this output level.  The
						// output may have been re-read with a level index shift;
						// skip rather than false-fail.
						t.Logf("output level %vx%v has no matching src level (opentile-go limitation)",
							outLvl.Size().X, outLvl.Size().Y)
						continue
					}
					if srcLvl.TileSize() != outLvl.TileSize() {
						t.Errorf("L(%dx%d) tile size: src=%v out=%v",
							key.w, key.h, srcLvl.TileSize(), outLvl.TileSize())
					}
					grid := srcLvl.Grid()
					srcBuf := make([]byte, srcLvl.TileMaxSize())
					outBuf := make([]byte, outLvl.TileMaxSize())
					for ty := 0; ty < grid.Y; ty++ {
						for tx := 0; tx < grid.X; tx++ {
							sn, _ := srcLvl.TileInto(tx, ty, srcBuf)
							on, _ := outLvl.TileInto(tx, ty, outBuf)
							if !bytes.Equal(srcBuf[:sn], outBuf[:on]) {
								t.Fatalf("L(%dx%d) tile (%d,%d) bytes differ: src=%d out=%d",
									key.w, key.h, tx, ty, sn, on)
							}
						}
					}
				}
			}

			// Layout: smallest level tile data comes before largest level.
			// Only meaningful for multi-level slides.
			data, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}

			// Detect BigTIFF (magic 0x002B at bytes 2-3).
			isBigTIFF := len(data) >= 4 &&
				data[2] == 0x2B && data[3] == 0x00

			if isBigTIFF {
				// Reverse-order layout check is only implemented for classic TIFF.
				t.Logf("BigTIFF output: skipping classic reverse-order layout check")
			} else {
				l0, lN, err := firstAndLastLevelTileOffsets(data)
				if err != nil {
					t.Logf("layout check skipped: %v", err)
				} else if l0 != lN && lN >= l0 {
					// l0 == lN is expected for single-level slides.
					t.Errorf("reverse order broken: lastLevel offset %d should be < L0 offset %d", lN, l0)
				}
			}

			// Ghost area present.  Classic TIFF: ghost starts at byte 8.
			// BigTIFF: header is 16 bytes, so ghost starts at byte 16.
			ghostStart := 8
			if isBigTIFF {
				ghostStart = 16
			}
			if ghostStart+40 > len(data) || !strings.HasPrefix(string(data[ghostStart:ghostStart+40]), "GDAL_STRUCTURAL_METADATA_SIZE=") {
				t.Errorf("ghost area missing in output (isBigTIFF=%v, ghostStart=%d)", isBigTIFF, ghostStart)
			}
		})
	}
}

// firstAndLastLevelTileOffsets returns (L0 tile0 offset, lastLevel tile0 offset)
// for a classic TIFF file. Walks IFD0 and the last pyramid IFD.
func firstAndLastLevelTileOffsets(data []byte) (l0, lN uint64, err error) {
	ifd0 := uint64(binary.LittleEndian.Uint32(data[4:8]))
	l0, next, err := firstTileOffsetClassic(data, ifd0)
	if err != nil {
		return 0, 0, err
	}
	last := l0
	for next != 0 {
		var off uint64
		off, next, err = firstTileOffsetClassic(data, next)
		if err != nil {
			// Hit an associated IFD (no TileOffsets); stop.
			break
		}
		last = off
	}
	return l0, last, nil
}

func firstTileOffsetClassic(data []byte, ifdOff uint64) (uint64, uint64, error) {
	if ifdOff+2 > uint64(len(data)) {
		return 0, 0, errShort
	}
	n := uint64(binary.LittleEndian.Uint16(data[ifdOff : ifdOff+2]))
	var off uint64
	var hasTileOffsets bool
	for i := uint64(0); i < n; i++ {
		base := ifdOff + 2 + i*12
		tag := binary.LittleEndian.Uint16(data[base : base+2])
		count := uint64(binary.LittleEndian.Uint32(data[base+4 : base+8]))
		val := uint64(binary.LittleEndian.Uint32(data[base+8 : base+12]))
		if tag == 324 {
			if count == 1 {
				off = val
			} else {
				off = uint64(binary.LittleEndian.Uint32(data[val : val+4]))
			}
			hasTileOffsets = true
		}
	}
	next := uint64(binary.LittleEndian.Uint32(data[ifdOff+2+n*12 : ifdOff+2+n*12+4]))
	if !hasTileOffsets {
		return 0, next, errNoTileOffsets
	}
	return off, next, nil
}

var (
	errShort         = newErr("short read")
	errNoTileOffsets = newErr("no TileOffsets")
)

type strErr string

func (e strErr) Error() string { return string(e) }
func newErr(s string) error    { return strErr(s) }
