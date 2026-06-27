package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wsilabs/wsitools/internal/codec"
)

// photometricOf returns the IFD0 PhotometricInterpretation value string ("RGB (2)"
// or "YCbCr (6)") from a written file.
func photometricOf(t *testing.T, bin, file string) string {
	t.Helper()
	ifd0 := dumpIFD0Raw(t, bin, file)
	for _, line := range strings.Split(ifd0, "\n") {
		if strings.Contains(line, "PhotometricInterpretation") {
			if i := strings.Index(line, "value="); i >= 0 {
				return strings.TrimSpace(line[i+len("value="):])
			}
		}
	}
	t.Fatalf("no PhotometricInterpretation in IFD0:\n%s", ifd0)
	return ""
}

// TestConvertPhotometricMatchesJPEGFraming guards the colour-correctness fix: a TIFF
// wrapping YCbCr JPEG tiles must tag Photometric=YCbCr(6) so Aperio-ecosystem
// readers (OpenSlide, ImageScope) apply YCbCr→RGB; an Aperio-framed (bare) JPEG
// keeps Photometric=RGB(2). Covers re-encode AND both verbatim tile-copy cases,
// seeding the JFIF-source case by re-encoding CMU (CI-available) first.
func TestConvertPhotometricMatchesJPEGFraming(t *testing.T) {
	bin := stripedBinary(t)
	src := stripedSample(t, "svs/CMU-1-Small-Region.svs")
	dir := t.TempDir()

	conv := func(out string, args ...string) {
		t.Helper()
		full := append([]string{"convert", "--to", "svs", "-f", "-o", out}, args...)
		full = append(full, src)
		if o, err := exec.Command(bin, full...).CombinedOutput(); err != nil {
			t.Fatalf("convert %v: %v\n%s", args, err, o)
		}
	}

	// 1. Re-encode to JPEG → wsitools emits a JFIF/YCbCr JPEG → Photometric=YCbCr.
	reenc := filepath.Join(dir, "reenc.svs")
	conv(reenc, "--codec", "jpeg")
	if got := photometricOf(t, bin, reenc); !strings.Contains(got, "YCbCr") {
		t.Errorf("re-encode JPEG: Photometric = %q, want YCbCr(6)", got)
	}

	// 2. Verbatim tile-copy of a JFIF source (the re-encoded SVS) → also YCbCr.
	srcSave := src
	src = reenc
	vjfif := filepath.Join(dir, "verbatim_jfif.svs")
	conv(vjfif) // no --codec → tile-copy path
	if got := photometricOf(t, bin, vjfif); !strings.Contains(got, "YCbCr") {
		t.Errorf("verbatim JFIF source: Photometric = %q, want YCbCr(6)", got)
	}
	src = srcSave

	// 3. Verbatim tile-copy of the Aperio-framed (bare-JPEG) CMU → stays RGB.
	vaperio := filepath.Join(dir, "verbatim_aperio.svs")
	conv(vaperio) // no --codec → tile-copy of the original Aperio SVS
	if got := photometricOf(t, bin, vaperio); !strings.Contains(got, "RGB") {
		t.Errorf("verbatim Aperio source: Photometric = %q, want RGB(2)", got)
	}
}

// TestJPEGTilePhotometric checks the JPEG-framing → TIFF-photometric mapping that
// keeps re-encoded/copied JPEG tiles rendering correctly in libtiff/Aperio readers
// (OpenSlide, ImageScope): a JPEG that self-declares YCbCr (JFIF, or Adobe
// transform 1/2) must be tagged YCbCr(6); a bare JPEG (Aperio framing) or an Adobe
// transform=0 (raw RGB) JPEG must be tagged RGB(2).
func TestJPEGTilePhotometric(t *testing.T) {
	soi := []byte{0xFF, 0xD8}
	sos := []byte{0xFF, 0xDA, 0x00, 0x02} // truncated SOS just to stop the scan
	// APP0 JFIF: FF E0, len=16, "JFIF\0" + 9 payload bytes.
	jfif := []byte{0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00}
	// APP14 Adobe: FF EE, len=14, "Adobe" + version(2) + flags0(2) + flags1(2) + transform(1).
	adobe := func(transform byte) []byte {
		return []byte{0xFF, 0xEE, 0x00, 0x0E, 'A', 'd', 'o', 'b', 'e', 0x00, 0x64, 0x00, 0x00, 0x00, 0x00, transform}
	}
	// SOF0: FF C0, len=17, precision, h(2), w(2), ncomp=3, then 3×(id,samp,qt).
	sof := func(id0, id1, id2 byte) []byte {
		return []byte{0xFF, 0xC0, 0x00, 0x11, 0x08, 0x00, 0x10, 0x00, 0x10, 0x03, id0, 0x22, 0x00, id1, 0x11, 0x00, id2, 0x11, 0x00}
	}

	cat := func(parts ...[]byte) []byte {
		var b []byte
		for _, p := range parts {
			b = append(b, p...)
		}
		return b
	}

	cases := []struct {
		name string
		tile []byte
		want uint16
	}{
		{"jfif → YCbCr", cat(soi, jfif, sos), codec.PhotometricYCbCr},
		{"adobe transform=0 → RGB", cat(soi, adobe(0), sos), codec.PhotometricRGB},
		{"adobe transform=1 → YCbCr", cat(soi, adobe(1), sos), codec.PhotometricYCbCr},
		// JFIF mandates YCbCr and outranks a stray Adobe transform=0 marker —
		// exactly the shape opentile produces for re-encoded abbreviated tiles.
		{"jfif wins over adobe transform=0", cat(soi, jfif, adobe(0), sos), codec.PhotometricYCbCr},
		{"jfif + adobe-0 + SOF 1,2,3 (reencoded tile shape)", cat(soi, jfif, sof(1, 2, 3), adobe(0), sos), codec.PhotometricYCbCr},
		// Abbreviated YCbCr tile (no JFIF, tables in tag 347) with standard IDs 1,2,3
		// — wsitools' own re-encode output — must still map to YCbCr.
		{"bare SOF ids 1,2,3 → YCbCr", cat(soi, sof(1, 2, 3), sos), codec.PhotometricYCbCr},
		{"bare SOF ids 0,1,2 (Aperio) → RGB", cat(soi, sof(0, 1, 2), sos), codec.PhotometricRGB},
		{"bare SOF ids R,G,B → RGB", cat(soi, sof('R', 'G', 'B'), sos), codec.PhotometricRGB},
		{"bare (no SOF) → RGB", cat(soi, sos), codec.PhotometricRGB},
		{"not a jpeg → RGB", []byte("not a jpeg at all"), codec.PhotometricRGB},
		{"empty → RGB", nil, codec.PhotometricRGB},
	}
	for _, c := range cases {
		if got := jpegTilePhotometric(c.tile); got != c.want {
			t.Errorf("%s: jpegTilePhotometric = %d, want %d", c.name, got, c.want)
		}
	}
}
