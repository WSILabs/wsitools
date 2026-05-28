//go:build !nocgo

package aperioapp14

import (
	"bytes"
	"testing"

	"github.com/wsilabs/wsitools/internal/codec"
)

func TestEncoderProducesAPP14Marker(t *testing.T) {
	enc, err := New(
		codec.LevelGeometry{TileWidth: 16, TileHeight: 16},
		codec.Quality{Knobs: map[string]string{"q": "85"}},
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer enc.Close()

	rgb := make([]byte, 16*16*3)
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			i := y*16*3 + x*3
			rgb[i+0] = byte(x * 16)
			rgb[i+1] = byte(y * 16)
			rgb[i+2] = 0
		}
	}

	body, err := enc.EncodeTile(rgb, 16, 16, nil)
	if err != nil {
		t.Fatalf("EncodeTile: %v", err)
	}

	found := false
	for i := 0; i+15 < len(body); i++ {
		if body[i] == 0xFF && body[i+1] == 0xEE {
			if bytes.Equal(body[i+4:i+9], []byte("Adobe")) {
				found = true
				break
			}
		}
	}
	if !found {
		t.Errorf("APP14 Adobe marker missing from encoded tile (len=%d)", len(body))
	}
}
