package main

import (
	"bytes"
	"fmt"
	"image/png"
	"log/slog"

	"github.com/wsilabs/opentile-go/decoder"
	"github.com/wsilabs/wsitools/internal/source"
)

// writeAssociatedPNGs decodes each of src's associated images to a lossless PNG
// and hands it to emit (a writer's WriteAssociated). DZI and SZI have no native
// slot for associated images, so rather than dropping them wsitools emits them
// as PNG sidecars (predictable <type>.png names; the consumer can re-encode if
// desired). No-op when --no-associated. A decode failure warns and skips that
// image rather than aborting the conversion.
func writeAssociatedPNGs(src source.Source, emit func(typ string, pngBytes []byte) error) error {
	if cvNoAssociated {
		return nil
	}
	for _, a := range src.Associated() {
		di, err := a.Decode(decoder.DecodeOptions{Format: decoder.PixelFormatRGB})
		if err != nil {
			slog.Warn("skipping associated image (decode failed)", "type", a.Type(), "err", err)
			continue
		}
		img := rgbToImage(packTightRGB(di), di.Width, di.Height)
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return fmt.Errorf("encode associated %s as png: %w", a.Type(), err)
		}
		if err := emit(a.Type(), buf.Bytes()); err != nil {
			return err
		}
	}
	return nil
}
