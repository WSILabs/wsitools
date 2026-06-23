package ife

import (
	"testing"

	codec "github.com/wsilabs/wsitools/internal/codec"
	jpegcodec "github.com/wsilabs/wsitools/internal/codec/jpeg"
)

var testJPEG256 []byte

func TestMain(m *testing.M) {
	enc, err := jpegcodec.New(codec.LevelGeometry{TileWidth: 256, TileHeight: 256, PixelFormat: codec.PixelFormatRGB8}, codec.Quality{Knobs: map[string]string{"q": "85"}})
	if err != nil {
		panic(err)
	}
	rgb := make([]byte, 256*256*3) // black tile
	testJPEG256, err = enc.EncodeStandalone(rgb, 256, 256)
	if err != nil {
		panic(err)
	}
	m.Run()
}

func solidTile(t *testing.T) []byte {
	t.Helper()
	return testJPEG256
}
