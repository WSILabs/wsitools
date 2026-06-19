package main

import (
	"bytes"
	stdjpeg "image/jpeg"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/wsitools/internal/codec"
	_ "github.com/wsilabs/wsitools/internal/codec/all"
)

func TestCodecTileEncoderAbbreviatedRoundTrip(t *testing.T) {
	fac, err := codec.Lookup("jpeg")
	if err != nil {
		t.Fatalf("lookup jpeg: %v", err)
	}
	enc, err := fac.NewEncoder(codec.LevelGeometry{TileWidth: 64, TileHeight: 64, PixelFormat: codec.PixelFormatRGB8}, codec.Quality{Knobs: map[string]string{"q": "80"}})
	if err != nil {
		t.Fatalf("new encoder: %v", err)
	}
	defer enc.Close()
	te := &codecTileEncoder{enc: enc}

	rgb := make([]byte, 64*64*3)
	for i := range rgb {
		rgb[i] = 128
	}
	body, err := te.EncodeTile(rgb, 64, 64)
	if err != nil {
		t.Fatalf("EncodeTile: %v", err)
	}
	// Abbreviated tile: stdlib JPEG decode FAILS without the tables (no DQT/DHT).
	if _, err := stdjpeg.Decode(bytes.NewReader(body)); err == nil {
		t.Errorf("expected abbreviated (table-less) JPEG to fail stdlib decode; it decoded — not abbreviated?")
	}
	// LevelHeader (tag 347) must be non-empty so the writer can supply the tables.
	if len(enc.LevelHeader()) == 0 {
		t.Errorf("LevelHeader empty; abbreviated tiles would be undecodable")
	}
}

func TestOctaveLevelSpecsForFloors(t *testing.T) {
	// 1000×800, tile 256 → flooredLevelCount=3; specs finest-first, Index==k,
	// overlap 0, octave dims.
	specs := octaveLevelSpecsFor(opentile.Size{W: 1000, H: 800}, 256)
	if len(specs) != 3 {
		t.Fatalf("levels = %d, want 3", len(specs))
	}
	if specs[0].Index != 0 || specs[0].Width != 1000 || specs[0].Height != 800 || specs[0].Overlap != 0 {
		t.Errorf("L0 = %+v, want Index0 1000×800 overlap0", specs[0])
	}
	if specs[1].Width != 500 || specs[1].Height != 400 {
		t.Errorf("L1 = %d×%d, want 500×400", specs[1].Width, specs[1].Height)
	}
	if specs[2].Width != 250 || specs[2].Height != 200 {
		t.Errorf("L2 = %d×%d, want 250×200", specs[2].Width, specs[2].Height)
	}
	if specs[0].TileW != 256 || specs[0].TileH != 256 {
		t.Errorf("tile = %d×%d, want 256", specs[0].TileW, specs[0].TileH)
	}
}

func TestFlooredLevelCount(t *testing.T) {
	cases := []struct {
		w, h, tile, want int
		note             string
	}{
		{1000, 800, 256, 3, "1000→500→250(≤256 stop): L0,1,2"},
		{256, 256, 256, 1, "already ≤ tile: single level"},
		{100, 100, 256, 1, "smaller than tile: single level"},
		{4096, 4096, 256, 5, "4096→2048→1024→512→256(≤256): 5 levels"},
		{300, 90, 256, 1, "min dim 90 ≤ 256 at L0: single level"},
		{4096, 4096, 512, 4, "4096→2048→1024→512(≤512): 4 levels"},
	}
	for _, c := range cases {
		if got := flooredLevelCount(c.w, c.h, c.tile); got != c.want {
			t.Errorf("flooredLevelCount(%d,%d,%d) = %d, want %d (%s)", c.w, c.h, c.tile, got, c.want, c.note)
		}
	}
}
