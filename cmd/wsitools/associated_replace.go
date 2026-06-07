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
	"github.com/wsilabs/wsitools/internal/tiff/cogwsiwriter"
	"github.com/wsilabs/wsitools/internal/tiff/edit"
)

const rowsPerStrip = 2

// tagWSIImageType is the wsitools/COG-WSI private tag (65080) carrying the
// associated-image type. opentile-go's generic-TIFF classifier treats it as
// authoritative (formats/generictiff/classifier.go).
const tagWSIImageType = 65080

// replaceOpts controls how buildReplacementIFD encodes the image.
type replaceOpts struct {
	typ         string     // label|macro|thumbnail|overview
	format      string     // source container: "svs" | "generic-tiff"
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

	var rep *edit.ReplacementIFD
	var err error
	switch codec {
	case "lzw":
		rep, err = buildLZWReplacementIFD(img, width, height, desc)
	case "jpeg":
		rep, err = buildJPEGReplacementIFD(img, width, height, desc)
	case "deflate":
		rep, err = buildDeflateReplacementIFD(img, width, height, desc)
	case "none":
		rep, err = buildRawReplacementIFD(img, width, height, desc)
	default:
		return nil, fmt.Errorf("unknown compression %q (want lzw, jpeg, deflate, none)", codec)
	}
	if err != nil {
		return nil, err
	}
	applyAssocMarkers(rep, o.format, o.typ)
	return rep, nil
}

// applyAssocMarkers stamps the new IFD with the signals each reader uses to
// classify an associated image by type, so a replaced/added image is read back
// as the intended type rather than misclassified.
//
//   - SVS classification is purely structural (opentile-go formats/svs/series.go):
//     a trailing non-tiled page is Macro iff NewSubfileType==9, else Label. The
//     codec builders default NewSubfileType=1 (correct for label/thumbnail); the
//     macro/overview image MUST be 9, or it is misread as a second label and the
//     real label slot is clobbered.
//   - generic-TIFF prefers the WSIImageType private tag (formats/generictiff/
//     classifier.go) over dimension/codec heuristics; emit it so the type is
//     authoritative (e.g. a JPEG-encoded "label" isn't guessed as a thumbnail).
//     It is ignored by the SVS reader, so emitting it unconditionally is safe.
func applyAssocMarkers(rep *edit.ReplacementIFD, format, typ string) {
	if format == "svs" && (typ == "macro" || typ == "overview") {
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, 9)
		for i := range rep.Tags {
			if rep.Tags[i].Tag == edit.TagNewSubfileType {
				rep.Tags[i].Bytes = b
			}
		}
	}
	wt := append([]byte(typ), 0)
	rep.Tags = append(rep.Tags, edit.OutTag{
		Tag: tagWSIImageType, Type: edit.TypeASCII, Count: uint64(len(wt)), Inline: false, Bytes: wt,
	})
	sortTags(rep.Tags)
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
	jfif, err := encodeJPEGWhole(img)
	if err != nil {
		return nil, err
	}
	strips := [][]byte{jfif}

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

// ---------- COG-WSI single-blob encoders + AssociatedSpec packaging ----------
//
// COG-WSI associated images are single-strip self-contained payloads (unlike the
// SVS Slice-1 splice, which uses 2-row strips). JPEG is a full JFIF; LZW/deflate/
// none is ONE whole-image strip.

// encodeJPEGWhole encodes img as a full baseline JFIF (quality 90). Shared by the
// SVS Slice-1 buildJPEGReplacementIFD and the COG-WSI AssociatedSpec packager.
func encodeJPEGWhole(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		return nil, fmt.Errorf("jpeg encode: %w", err)
	}
	return buf.Bytes(), nil
}

// encodeLZWWhole returns a single whole-image LZW stream (no predictor).
//
// The COG-WSI associated-image IFD written by cogwsiwriter.populateAssocIFD does
// NOT emit a Predictor (317) tag, and the opentile-go reader does not apply
// horizontal differencing on read-back — so the bytes must be LZW-compressed RGB
// WITHOUT predictor, matching how convert --to cog-wsi writes LZW associated
// images. (This differs from the SVS Slice-1 path, where the IFD does carry
// Predictor=2.)
func encodeLZWWhole(img image.Image) []byte {
	return encodeLZW(rgbStripBytes(img))
}

// encodeDeflateWhole returns a single whole-image zlib stream (no predictor); see
// encodeLZWWhole for why predictor is omitted.
func encodeDeflateWhole(img image.Image) []byte {
	var buf bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&buf, zlib.BestCompression)
	_, _ = zw.Write(rgbStripBytes(img))
	_ = zw.Close()
	return buf.Bytes()
}

// buildReplacementAssocSpec encodes img as a cogwsiwriter.AssociatedSpec for the
// given type/options. COG-WSI associated images are single-strip self-contained
// payloads, so JPEG is a full JFIF and LZW/deflate/none is one whole-image strip.
func buildReplacementAssocSpec(img image.Image, o replaceOpts) (*cogwsiwriter.AssociatedSpec, error) {
	codec := o.compression
	if codec == "" {
		if o.typ == "label" {
			codec = "lzw"
		} else {
			codec = "jpeg"
		}
	}
	if o.targetW != 0 || o.targetH != 0 {
		prepared, err := fitImage(img, o)
		if err != nil {
			return nil, err
		}
		img = prepared
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()

	var payload []byte
	var compTag uint16
	switch codec {
	case "jpeg":
		buf, err := encodeJPEGWhole(img)
		if err != nil {
			return nil, err
		}
		payload, compTag = buf, 7
	case "lzw":
		payload, compTag = encodeLZWWhole(img), 5
	case "deflate":
		payload, compTag = encodeDeflateWhole(img), 8
	case "none":
		payload, compTag = rgbStripBytes(img), 1
	default:
		return nil, fmt.Errorf("unknown compression %q (want jpeg, lzw, deflate, none)", codec)
	}

	return &cogwsiwriter.AssociatedSpec{
		Type:            o.typ,
		Width:           uint32(w),
		Height:          uint32(h),
		Compression:     compTag,
		Photometric:     2,
		SamplesPerPixel: 3,
		BitsPerSample:   []uint16{8, 8, 8},
		Bytes:           payload,
	}, nil
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
