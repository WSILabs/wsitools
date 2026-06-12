package dicomwriter

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/wsilabs/wsitools/internal/source"
)

// j2kStream: SOC + segments + SOD (enough for a main-header parse).
func j2kStream(segs ...[]byte) []byte {
	out := []byte{0xFF, 0x4F}
	for _, s := range segs {
		out = append(out, s...)
	}
	return append(out, 0xFF, 0x93) // SOD
}

// siz builds a SIZ marker (FF51) with the given component count + 8-bit-style
// precision (precision byte is encoded as (precision-1)&0x7F in Ssiz).
func siz(components int, precision byte) []byte {
	body := make([]byte, 36) // Rsiz(2)+8×4 fields(32)+Csiz(2) → Csiz at [34:36]
	binary.BigEndian.PutUint16(body[34:36], uint16(components))
	for c := 0; c < components; c++ {
		body = append(body, (precision-1)&0x7F, 0x01, 0x01) // Ssiz, XRsiz, YRsiz
	}
	segLen := 2 + len(body)
	seg := []byte{0xFF, 0x51, byte(segLen >> 8), byte(segLen)}
	return append(seg, body...)
}

// cod builds a COD marker (FF52) with the given MCT (0/1) and transform
// (0=irreversible/lossy, 1=reversible/lossless).
func cod(mct, transform byte) []byte {
	body := make([]byte, 10) // Scod,prog,layers(2),MCT,decomp,cbW,cbH,cbStyle,transform
	body[4] = mct
	body[9] = transform
	segLen := 2 + len(body)
	seg := []byte{0xFF, 0x52, byte(segLen >> 8), byte(segLen)}
	return append(seg, body...)
}

func TestInspectJP2K_RGB(t *testing.T) {
	info, err := InspectJP2K(j2kStream(siz(3, 8), cod(0, 0)))
	if err != nil {
		t.Fatalf("InspectJP2K: %v", err)
	}
	if info.Components != 3 || info.Precision != 8 || info.MCT || info.Reversible {
		t.Errorf("got %+v, want comps=3 prec=8 MCT=false Reversible=false", info)
	}
	if p, _ := PhotometricJP2K(info); p != "RGB" {
		t.Errorf("Photometric = %q, want RGB", p)
	}
}

func TestInspectJP2K_YBR_ICT(t *testing.T) {
	info, _ := InspectJP2K(j2kStream(siz(3, 8), cod(1, 0))) // MCT + irreversible
	if !info.MCT || info.Reversible {
		t.Errorf("got MCT=%v Reversible=%v, want true/false", info.MCT, info.Reversible)
	}
	if p, _ := PhotometricJP2K(info); p != "YBR_ICT" {
		t.Errorf("Photometric = %q, want YBR_ICT", p)
	}
}

func TestInspectJP2K_YBR_RCT(t *testing.T) {
	info, _ := InspectJP2K(j2kStream(siz(3, 8), cod(1, 1))) // MCT + reversible
	if p, _ := PhotometricJP2K(info); p != "YBR_RCT" {
		t.Errorf("Photometric = %q, want YBR_RCT", p)
	}
	if !info.Reversible {
		t.Errorf("Reversible = false, want true")
	}
}

func TestInspectJP2K_Mono(t *testing.T) {
	info, _ := InspectJP2K(j2kStream(siz(1, 8), cod(0, 0)))
	if p, _ := PhotometricJP2K(info); p != "MONOCHROME2" {
		t.Errorf("Photometric = %q, want MONOCHROME2", p)
	}
}

func TestInspectJP2K_Errors(t *testing.T) {
	if _, err := InspectJP2K([]byte{0x00, 0x01}); err == nil {
		t.Error("want error for non-SOC input")
	}
	if _, err := InspectJP2K(j2kStream(siz(3, 8))); err == nil {
		t.Error("want error when COD is missing")
	}
	if _, err := PhotometricJP2K(JP2KInfo{Components: 3, Precision: 12}); err == nil {
		t.Error("want error for precision != 8")
	}
	if _, err := PhotometricJP2K(JP2KInfo{Components: 2, Precision: 8}); err == nil {
		t.Error("want error for component count 2")
	}
}

// Real-fixture sanity: JP2K-33003-1.svs L0 is 3-component, 8-bit, MCT=0, lossy.
func TestInspectJP2K_RealFixture(t *testing.T) {
	dir := os.Getenv("WSI_TOOLS_TESTDIR")
	if dir == "" {
		dir = "../../sample_files"
	}
	p := filepath.Join(dir, "svs", "JP2K-33003-1.svs")
	if _, err := os.Stat(p); err != nil {
		t.Skip("no JP2K fixture")
	}
	src, err := source.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	lvl := src.Levels()[0]
	buf := make([]byte, lvl.TileMaxSize())
	n, err := lvl.TileInto(0, 0, buf)
	if err != nil {
		t.Fatal(err)
	}
	info, err := InspectJP2K(buf[:n])
	if err != nil {
		t.Fatalf("InspectJP2K(real tile): %v", err)
	}
	if info.Components != 3 || info.Precision != 8 || info.MCT {
		t.Errorf("real fixture: got %+v, want comps=3 prec=8 MCT=false", info)
	}
	if p, _ := PhotometricJP2K(info); p != "RGB" {
		t.Errorf("real fixture Photometric = %q, want RGB", p)
	}
}
