package main

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	stdraw "image/draw"
	"image/jpeg"
	"math"

	xdraw "golang.org/x/image/draw"

	"github.com/hhrutter/lzw"
	"github.com/wsilabs/wsitools/internal/tiff/edit"
)

const rowsPerStrip = 2

// replaceOpts controls how buildReplacementIFD encodes the image.
type replaceOpts struct {
	typ         string     // label|macro|thumbnail|overview
	compression string     // "", "jpeg", "lzw", "deflate", "none"
	desc        string     // ImageDescription to write (may be empty)
	resize      string     // "fit"|"stretch"|"none" ("" => "fit")
	bg          color.RGBA // letterbox fill for fit
	targetW     int        // 0 => use image bounds
	targetH     int        // 0 => use image bounds
	force       bool       // bypass aspect-ratio guard
}

// buildReplacementIFD encodes img as a TIFF-ready IFD for use with edit.Splice.
func buildReplacementIFD(img image.Image, o replaceOpts) (*edit.ReplacementIFD, error) {
	// Resolve codec.
	codec := o.compression
	if codec == "" {
		if o.typ == "label" {
			codec = "lzw"
		} else {
			codec = "jpeg"
		}
	}

	// Resize / letterbox if requested.
	if o.targetW != 0 || o.targetH != 0 {
		prepared, err := fitImage(img, o)
		if err != nil {
			return nil, err
		}
		img = prepared
	}

	b := img.Bounds()
	width, height := b.Dx(), b.Dy()

	// Build ImageDescription.
	desc := o.desc
	if desc == "" {
		desc = fmt.Sprintf("%s %dx%d", o.typ, width, height)
	}

	switch codec {
	case "lzw":
		return buildLZWReplacementIFD(img, width, height, desc)
	case "jpeg":
		return buildJPEGReplacementIFD(img, width, height, desc)
	case "deflate":
		return buildDeflateReplacementIFD(img, width, height, desc)
	case "none":
		return buildRawReplacementIFD(img, width, height, desc)
	default:
		return nil, fmt.Errorf("unknown compression %q (want lzw, jpeg, deflate, none)", codec)
	}
}

// ---------- codec: LZW + predictor 2 ----------

func buildLZWReplacementIFD(img image.Image, width, height int, desc string) (*edit.ReplacementIFD, error) {
	strips := encodeLZWStrips(img)

	tags := buildBaseTagSet(width, height, desc, 5 /*LZW*/, strips)
	// Add predictor=2.
	tags = append(tags, shortTag(edit.TagPredictor, 2))
	sortTags(tags)

	return &edit.ReplacementIFD{Tags: tags, StripData: strips}, nil
}

// encodeLZWStrips splits img into 2-row strips, applies TIFF predictor 2, and
// LZW-compresses each strip using the early-change (libtiff-compatible) variant.
func encodeLZWStrips(img image.Image) [][]byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	raw := rgbStripBytes(img)
	stride := w * 3

	numStrips := (h + rowsPerStrip - 1) / rowsPerStrip
	strips := make([][]byte, numStrips)
	for s := 0; s < numStrips; s++ {
		y0 := s * rowsPerStrip
		y1 := y0 + rowsPerStrip
		if y1 > h {
			y1 = h
		}
		stripRaw := raw[y0*stride : y1*stride]
		stripPre := applyPredictor2(stripRaw, w, y1-y0)
		strips[s] = encodeLZW(stripPre)
	}
	return strips
}

// ---------- codec: JPEG ----------

func buildJPEGReplacementIFD(img image.Image, width, height int, desc string) (*edit.ReplacementIFD, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		return nil, fmt.Errorf("jpeg encode: %w", err)
	}
	strips := [][]byte{buf.Bytes()}

	shortLE := func(v uint16) []byte {
		b := make([]byte, 4)
		binary.LittleEndian.PutUint16(b, v)
		return b
	}
	longLE := func(v uint32) []byte {
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, v)
		return b
	}

	bps := make([]byte, 6)
	binary.LittleEndian.PutUint16(bps[0:2], 8)
	binary.LittleEndian.PutUint16(bps[2:4], 8)
	binary.LittleEndian.PutUint16(bps[4:6], 8)

	byteCountsBytes := longLE(uint32(len(strips[0])))

	descBytes := append([]byte(desc), 0)

	tags := []edit.OutTag{
		{Tag: edit.TagNewSubfileType, Type: edit.TypeLong, Count: 1, Inline: true, Bytes: longLE(1)},
		{Tag: edit.TagImageWidth, Type: edit.TypeLong, Count: 1, Inline: true, Bytes: longLE(uint32(width))},
		{Tag: edit.TagImageLength, Type: edit.TypeLong, Count: 1, Inline: true, Bytes: longLE(uint32(height))},
		{Tag: edit.TagBitsPerSample, Type: edit.TypeShort, Count: 3, Inline: false, Bytes: bps},
		{Tag: edit.TagCompression, Type: edit.TypeShort, Count: 1, Inline: true, Bytes: shortLE(7)},
		{Tag: edit.TagPhotometric, Type: edit.TypeShort, Count: 1, Inline: true, Bytes: shortLE(2)},
		{Tag: edit.TagImageDescription, Type: edit.TypeASCII, Count: uint64(len(descBytes)), Inline: false, Bytes: descBytes},
		{Tag: edit.TagStripOffsets, Type: edit.TypeLong, Count: 1, Inline: false, Bytes: make([]byte, 4), ResolvesToOffset: true, OffsetRefs: []int{0}},
		{Tag: edit.TagSamplesPerPixel, Type: edit.TypeShort, Count: 1, Inline: true, Bytes: shortLE(3)},
		{Tag: edit.TagRowsPerStrip, Type: edit.TypeLong, Count: 1, Inline: true, Bytes: longLE(uint32(height))},
		{Tag: edit.TagStripByteCounts, Type: edit.TypeLong, Count: 1, Inline: true, Bytes: byteCountsBytes},
		{Tag: edit.TagPlanarConfig, Type: edit.TypeShort, Count: 1, Inline: true, Bytes: shortLE(1)},
	}
	sortTags(tags)

	return &edit.ReplacementIFD{Tags: tags, StripData: strips}, nil
}

// ---------- codec: deflate ----------

func buildDeflateReplacementIFD(img image.Image, width, height int, desc string) (*edit.ReplacementIFD, error) {
	numStrips := (height + rowsPerStrip - 1) / rowsPerStrip
	strips := make([][]byte, numStrips)
	raw := rgbStripBytes(img)
	stride := width * 3

	for s := 0; s < numStrips; s++ {
		y0 := s * rowsPerStrip
		y1 := y0 + rowsPerStrip
		if y1 > height {
			y1 = height
		}
		stripRaw := raw[y0*stride : y1*stride]
		stripPre := applyPredictor2(stripRaw, width, y1-y0)
		var buf bytes.Buffer
		w, _ := zlib.NewWriterLevel(&buf, zlib.BestCompression)
		_, _ = w.Write(stripPre)
		_ = w.Close()
		strips[s] = buf.Bytes()
	}

	tags := buildBaseTagSet(width, height, desc, 8 /*Deflate*/, strips)
	tags = append(tags, shortTag(edit.TagPredictor, 2))
	sortTags(tags)

	return &edit.ReplacementIFD{Tags: tags, StripData: strips}, nil
}

// ---------- codec: none (raw RGB) ----------

func buildRawReplacementIFD(img image.Image, width, height int, desc string) (*edit.ReplacementIFD, error) {
	numStrips := (height + rowsPerStrip - 1) / rowsPerStrip
	strips := make([][]byte, numStrips)
	raw := rgbStripBytes(img)
	stride := width * 3

	for s := 0; s < numStrips; s++ {
		y0 := s * rowsPerStrip
		y1 := y0 + rowsPerStrip
		if y1 > height {
			y1 = height
		}
		dst := make([]byte, (y1-y0)*stride)
		copy(dst, raw[y0*stride:y1*stride])
		strips[s] = dst
	}

	tags := buildBaseTagSet(width, height, desc, 1 /*None*/, strips)
	sortTags(tags)

	return &edit.ReplacementIFD{Tags: tags, StripData: strips}, nil
}

// ---------- shared tag helpers ----------

// buildBaseTagSet builds the common tag list for lzw/deflate/raw codecs
// (multi-strip, 2 rows/strip). compressionVal is the TIFF compression code.
// Predictor is NOT included here — callers add it if needed.
func buildBaseTagSet(width, height int, desc string, compressionVal uint16, strips [][]byte) []edit.OutTag {
	shortLE := func(v uint16) []byte {
		b := make([]byte, 4)
		binary.LittleEndian.PutUint16(b, v)
		return b
	}
	longLE := func(v uint32) []byte {
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, v)
		return b
	}

	bps := make([]byte, 6)
	binary.LittleEndian.PutUint16(bps[0:2], 8)
	binary.LittleEndian.PutUint16(bps[2:4], 8)
	binary.LittleEndian.PutUint16(bps[4:6], 8)

	// StripByteCounts: one LONG per strip.
	byteCountsBytes := make([]byte, len(strips)*4)
	for i, s := range strips {
		binary.LittleEndian.PutUint32(byteCountsBytes[i*4:], uint32(len(s)))
	}

	// StripOffsets: placeholder, resolved at emit.
	offsetRefs := iotaSlice(len(strips))
	stripOffsetsPlaceholder := make([]byte, len(strips)*4)

	descBytes := append([]byte(desc), 0)

	tags := []edit.OutTag{
		{Tag: edit.TagNewSubfileType, Type: edit.TypeLong, Count: 1, Inline: true, Bytes: longLE(1)},
		{Tag: edit.TagImageWidth, Type: edit.TypeLong, Count: 1, Inline: true, Bytes: longLE(uint32(width))},
		{Tag: edit.TagImageLength, Type: edit.TypeLong, Count: 1, Inline: true, Bytes: longLE(uint32(height))},
		{Tag: edit.TagBitsPerSample, Type: edit.TypeShort, Count: 3, Inline: false, Bytes: bps},
		{Tag: edit.TagCompression, Type: edit.TypeShort, Count: 1, Inline: true, Bytes: shortLE(compressionVal)},
		{Tag: edit.TagPhotometric, Type: edit.TypeShort, Count: 1, Inline: true, Bytes: shortLE(2)},
		{Tag: edit.TagImageDescription, Type: edit.TypeASCII, Count: uint64(len(descBytes)), Inline: false, Bytes: descBytes},
		{Tag: edit.TagStripOffsets, Type: edit.TypeLong, Count: uint64(len(strips)), Inline: false, Bytes: stripOffsetsPlaceholder, ResolvesToOffset: true, OffsetRefs: offsetRefs},
		{Tag: edit.TagSamplesPerPixel, Type: edit.TypeShort, Count: 1, Inline: true, Bytes: shortLE(3)},
		{Tag: edit.TagRowsPerStrip, Type: edit.TypeShort, Count: 1, Inline: true, Bytes: shortLE(uint16(rowsPerStrip))},
		{Tag: edit.TagStripByteCounts, Type: edit.TypeLong, Count: uint64(len(strips)), Inline: false, Bytes: byteCountsBytes},
		{Tag: edit.TagPlanarConfig, Type: edit.TypeShort, Count: 1, Inline: true, Bytes: shortLE(1)},
	}
	return tags
}

func shortTag(tag uint16, val uint16) edit.OutTag {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint16(b, val)
	return edit.OutTag{Tag: tag, Type: edit.TypeShort, Count: 1, Inline: true, Bytes: b}
}

// sortTags sorts a tag slice by tag number (ascending), as required by TIFF spec.
func sortTags(tags []edit.OutTag) {
	// Simple insertion sort — tag count is always small (<20).
	for i := 1; i < len(tags); i++ {
		for j := i; j > 0 && tags[j].Tag < tags[j-1].Tag; j-- {
			tags[j], tags[j-1] = tags[j-1], tags[j]
		}
	}
}

func iotaSlice(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

// ---------- low-level encoding primitives (ported from wsi-label-ref) ----------

// rgbStripBytes returns tightly packed 8-bit RGB bytes for img, stride=width*3.
func rgbStripBytes(img image.Image) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := make([]byte, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			out[(y*w+x)*3+0] = uint8(r >> 8)
			out[(y*w+x)*3+1] = uint8(g >> 8)
			out[(y*w+x)*3+2] = uint8(bl >> 8)
		}
	}
	return out
}

// applyPredictor2 performs TIFF horizontal differencing on packed RGB bytes.
func applyPredictor2(rgb []byte, w, h int) []byte {
	out := make([]byte, len(rgb))
	stride := w * 3
	for y := 0; y < h; y++ {
		row := rgb[y*stride : (y+1)*stride]
		orow := out[y*stride : (y+1)*stride]
		copy(orow[:3], row[:3])
		for x := 1; x < w; x++ {
			for c := 0; c < 3; c++ {
				orow[x*3+c] = row[x*3+c] - row[(x-1)*3+c]
			}
		}
	}
	return out
}

// encodeLZW compresses data with TIFF 6.0 LZW (early-change / oneOff=true).
func encodeLZW(data []byte) []byte {
	var buf bytes.Buffer
	w := lzw.NewWriter(&buf, true)
	_, _ = w.Write(data)
	_ = w.Close()
	return buf.Bytes()
}

// ---------- resize / letterbox ----------

const aspectMismatchThreshold = 2.0

// fitImage applies resize/letterbox to img according to opts.
func fitImage(img image.Image, o replaceOpts) (image.Image, error) {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	tw, th := o.targetW, o.targetH

	mode := o.resize
	if mode == "" {
		mode = "fit"
	}

	switch mode {
	case "none":
		if sw != tw || sh != th {
			return nil, fmt.Errorf("image is %dx%d but target is %dx%d and resize=none", sw, sh, tw, th)
		}
		return img, nil

	case "stretch":
		dst := image.NewRGBA(image.Rect(0, 0, tw, th))
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Over, nil)
		return dst, nil

	default: // "fit"
		if !o.force {
			srcAspect := float64(sw) / float64(sh)
			dstAspect := float64(tw) / float64(th)
			ratio := math.Max(srcAspect/dstAspect, dstAspect/srcAspect)
			if ratio > aspectMismatchThreshold {
				return nil, fmt.Errorf(
					"aspect mismatch: source %dx%d (%.2f), target %dx%d (%.2f), ratio %.2f > %.1f; use --force to override",
					sw, sh, srcAspect, tw, th, dstAspect, ratio, aspectMismatchThreshold,
				)
			}
		}
		return fitTo(img, tw, th, o.bg), nil
	}
}

// fitTo returns a tw×th RGBA image with img aspect-preservingly scaled to fit,
// letterboxed with bg.
func fitTo(src image.Image, tw, th int, bg color.RGBA) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	stdraw.Draw(dst, dst.Bounds(), &image.Uniform{C: bg}, image.Point{}, stdraw.Src)

	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw == tw && sh == th {
		stdraw.Draw(dst, dst.Bounds(), src, sb.Min, stdraw.Src)
		return dst
	}

	scale := float64(tw) / float64(sw)
	if s := float64(th) / float64(sh); s < scale {
		scale = s
	}
	dw := int(float64(sw) * scale)
	dh := int(float64(sh) * scale)
	ox := (tw - dw) / 2
	oy := (th - dh) / 2
	target := image.Rect(ox, oy, ox+dw, oy+dh)

	xdraw.CatmullRom.Scale(dst, target, src, sb, xdraw.Over, nil)
	return dst
}
