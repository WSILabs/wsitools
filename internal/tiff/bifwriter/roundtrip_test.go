package bifwriter

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/wsilabs/wsitools/internal/source"
)

func fixtureDir(t *testing.T) string {
	t.Helper()
	d := os.Getenv("WSI_TOOLS_TESTDIR")
	if d == "" {
		d = "../../../sample_files"
	}
	if _, err := os.Stat(d); err != nil {
		t.Skipf("fixtures unavailable (%s): %v", d, err)
	}
	return d
}

// bifLevel parses a written BigTIFF BIF and returns the pyramid (level=…) IFD's
// geometry plus the raw bytes of each stored tile, indexed by TILE_OFFSETS
// position. This is READER-INDEPENDENT: it reads the tile-offset array directly,
// so it verifies our on-disk byte order without going through any reader's
// spatial mapping (notably NOT opentile, whose serpentine remap is buggy for
// real row-major DP 200 files).
type bifLevel struct {
	w, h, tw, th int
	tileBytes    [][]byte // indexed by raw TILE_OFFSETS position
}

func parseBIFLevel(t *testing.T, path string) bifLevel {
	t.Helper()
	f, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(f) < 16 || f[0] != 0x49 || f[1] != 0x49 || f[2] != 0x2B {
		t.Fatalf("not little-endian BigTIFF: % x", f[:4])
	}
	off := binary.LittleEndian.Uint64(f[8:])
	for off != 0 {
		n := binary.LittleEndian.Uint64(f[off:])
		entries := map[uint16][3]uint64{} // tag -> {type, count, value/offset slot}
		p := off + 8
		for i := uint64(0); i < n; i++ {
			tag := binary.LittleEndian.Uint16(f[p:])
			typ := binary.LittleEndian.Uint16(f[p+2:])
			cnt := binary.LittleEndian.Uint64(f[p+4:])
			val := binary.LittleEndian.Uint64(f[p+12:])
			entries[tag] = [3]uint64{uint64(typ), cnt, val}
			p += 20
		}
		next := binary.LittleEndian.Uint64(f[p:])

		desc := entries[270]
		isPyramid := false
		if desc[1] > 0 {
			d := f[desc[2] : desc[2]+desc[1]]
			isPyramid = bytes.HasPrefix(d, []byte("level="))
		}
		if isPyramid {
			short := func(tag uint16) int { return int(entries[tag][2] & 0xFFFF) }
			long := func(tag uint16) int { return int(entries[tag][2] & 0xFFFFFFFF) }
			long8Arr := func(tag uint16) []uint64 {
				e := entries[tag]
				out := make([]uint64, e[1])
				for i := range out {
					out[i] = binary.LittleEndian.Uint64(f[e[2]+8*uint64(i):])
				}
				return out
			}
			offs := long8Arr(324)
			cnts := long8Arr(325)
			tb := make([][]byte, len(offs))
			for i := range offs {
				tb[i] = f[offs[i] : offs[i]+cnts[i]]
			}
			return bifLevel{
				w: long(256), h: long(257), tw: short(322), th: short(323),
				tileBytes: tb,
			}
		}
		off = next
	}
	t.Fatalf("no pyramid (level=) IFD found in %s", path)
	return bifLevel{}
}

func ceilDivT(a, b int) int { return (a + b - 1) / b }

// TestRowMajorTilePlacement: the written pyramid stores tiles ROW-MAJOR
// top-left — i.e. the compressed bytes at TILE_OFFSETS[row*cols+col] equal the
// source's compressed tile (col,row). This matches real Roche DP 200 (verified
// via Ventana-1.bif's own <Frame> nodes) and is what bio-formats/QuPath expect.
func TestRowMajorTilePlacement(t *testing.T) {
	src, err := source.Open(filepath.Join(fixtureDir(t), "svs", "CMU-1-Small-Region.svs"))
	if err != nil {
		t.Skipf("open source: %v", err)
	}
	defer src.Close()
	lvl := src.Levels()[0]

	out := filepath.Join(t.TempDir(), "rm.bif")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSingleLevel(f, FromLevel(lvl), IScanMeta{Magnification: 40, ScanRes: 0.25}); err != nil {
		f.Close()
		t.Fatalf("WriteSingleLevel: %v", err)
	}
	f.Close()

	bl := parseBIFLevel(t, out)
	cols := ceilDivT(lvl.Size().X, lvl.TileSize().X)
	rows := ceilDivT(lvl.Size().Y, lvl.TileSize().Y)
	if bl.tw != lvl.TileSize().X || bl.th != lvl.TileSize().Y {
		t.Fatalf("tile size %dx%d != source %dx%d", bl.tw, bl.th, lvl.TileSize().X, lvl.TileSize().Y)
	}
	if len(bl.tileBytes) != cols*rows {
		t.Fatalf("tile count %d != %d", len(bl.tileBytes), cols*rows)
	}
	buf := make([]byte, lvl.TileMaxSize())
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			n, err := lvl.TileInto(col, row, buf)
			if err != nil {
				t.Fatalf("source TileInto(%d,%d): %v", col, row, err)
			}
			want := buf[:n]
			got := bl.tileBytes[row*cols+col] // ROW-MAJOR index
			if !bytes.Equal(got, want) {
				t.Fatalf("tile (%d,%d): stored bytes at row-major index %d (len %d) != source (len %d) — not row-major?",
					col, row, row*cols+col, len(got), len(want))
			}
		}
	}
}

// TestWrittenBIFOpensAsBIF: a written BIF is still detected as bif by the
// reader stack (format/marker correctness), independent of opentile's buggy
// serpentine placement.
func TestWrittenBIFOpensAsBIF(t *testing.T) {
	src, err := source.Open(filepath.Join(fixtureDir(t), "svs", "CMU-1-Small-Region.svs"))
	if err != nil {
		t.Skipf("open source: %v", err)
	}
	defer src.Close()
	out := filepath.Join(t.TempDir(), "open.bif")
	f, _ := os.Create(out)
	if err := WriteSingleLevel(f, FromLevel(src.Levels()[0]), IScanMeta{Magnification: 40, ScanRes: 0.25}); err != nil {
		f.Close()
		t.Fatalf("WriteSingleLevel: %v", err)
	}
	f.Close()
	got, err := source.Open(out)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer got.Close()
	if got.Format() != "bif" {
		t.Fatalf("detected %q, want bif", got.Format())
	}
}
