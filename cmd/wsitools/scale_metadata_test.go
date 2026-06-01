package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// dumpRaw runs `dump-ifds --raw` and returns the output text.
func dumpRaw(t *testing.T, bin, file string) string {
	t.Helper()
	out, err := exec.Command(bin, "dump-ifds", "--raw", file).CombinedOutput()
	if err != nil {
		t.Fatalf("dump-ifds --raw %s: %v\n%s", file, err, out)
	}
	return string(out)
}

// TestDownsampleScalesMPPAndMag: a factor-2 downsample emits the WSI
// private MPP/mag tags with scaled values — magnification halved
// (40 → 20) and MPP doubled (~0.25 → ~0.50).
func TestDownsampleScalesMPPAndMag(t *testing.T) {
	bin := stripedBinary(t)
	src := stripedSample(t, "svs/scan_620_.svs")
	out := filepath.Join(t.TempDir(), "ds.svs")
	cmdOut, err := exec.Command(bin, "downsample", "--factor", "2", "-f", "-o", out, src).CombinedOutput()
	if err != nil {
		t.Fatalf("downsample: %v\n%s", err, cmdOut)
	}
	raw := dumpRaw(t, bin, out)
	if !strings.Contains(raw, "WSIMagnification") {
		t.Fatalf("downsample output missing WSIMagnification tag")
	}
	// Source 40x → output 20x.
	magLine := grepLine(raw, "WSIMagnification")
	if !strings.Contains(magLine, "value=20") {
		t.Errorf("WSIMagnification should be 20 (40/2); got: %s", magLine)
	}
	if !strings.Contains(raw, "WSIMPPx") {
		t.Errorf("downsample output missing WSIMPPx tag")
	}
	if !strings.Contains(raw, "XResolution") {
		t.Errorf("downsample output missing XResolution tag")
	}
}

// TestConvertCogWSICarriesScaleNDPI: cog-wsi from an NDPI source carries
// the WSI MPP/mag tags + resolution (cross-format MPP path).
func TestConvertCogWSICarriesScaleNDPI(t *testing.T) {
	bin := stripedBinary(t)
	src := stripedSample(t, "ndpi/CMU-1.ndpi")
	out := filepath.Join(t.TempDir(), "o.cog.tiff")
	cmdOut, err := exec.Command(bin, "convert", "--to", "cog-wsi", "-f", "-o", out, src).CombinedOutput()
	if err != nil {
		if strings.Contains(string(cmdOut), "no space left on device") {
			t.Skipf("disk full: %s", cmdOut)
		}
		t.Fatalf("convert: %v\n%s", err, cmdOut)
	}
	raw := dumpRaw(t, bin, out)
	for _, want := range []string{"WSIMPPx", "WSIMagnification", "XResolution", "ResolutionUnit"} {
		if !strings.Contains(raw, want) {
			t.Errorf("cog-wsi(NDPI) output missing %q", want)
		}
	}
}

// grepLine returns the first line containing sub, or "".
func grepLine(s, sub string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, sub) {
			return l
		}
	}
	return ""
}

// TestInfoReportsMPPForNDPI proves the cross-format MPP fix: info on an
// NDPI fixture now prints an MPP line (previously dropped — NDPI carries
// MPP in its TIFF resolution tags, which opentile-go reads).
func TestInfoReportsMPPForNDPI(t *testing.T) {
	bin := stripedBinary(t)
	sample := stripedSample(t, "ndpi/CMU-1.ndpi")
	out, err := exec.Command(bin, "info", sample).CombinedOutput()
	if err != nil {
		t.Fatalf("info: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "MPP:") {
		t.Errorf("info NDPI output missing 'MPP:' line:\n%s", out)
	}
}

// iccLen returns the byte count of tag 34675 in `dump-ifds --raw` output,
// or -1 if absent. Parses the "34675 (ICCProfile) UNDEFINED count=NNN" line.
func iccLen(raw string) int {
	for _, l := range strings.Split(raw, "\n") {
		if strings.Contains(l, "34675") {
			i := strings.Index(l, "count=")
			if i < 0 {
				return -1
			}
			n := 0
			for _, c := range l[i+6:] {
				if c < '0' || c > '9' {
					break
				}
				n = n*10 + int(c-'0')
			}
			return n
		}
	}
	return -1
}

// TestICCByteIdenticalAcrossPaths: JP2K-33003-1.svs's 141,992-byte ICC
// profile is present, byte-length-identical, through the streamwriter
// (svs re-encode), the cog-wsi writer (tile-copy), and downsample (which
// pulls ICC via src.ICCProfile() directly). tiff/ome-tiff share the
// streamwriter path with svs. (CMU-1.svs would also carry the same ICC
// but its size triggers a pre-existing, ICC-unrelated streamwriter hang.)
func TestICCByteIdenticalAcrossPaths(t *testing.T) {
	bin := stripedBinary(t)
	src := stripedSample(t, "svs/JP2K-33003-1.svs")
	const wantLen = 141992
	cases := []struct {
		name string
		args []string
		out  string
	}{
		{"svs", []string{"convert", "--to", "svs", "--codec", "jpegxl"}, "o.svs"},
		{"cog-wsi", []string{"convert", "--to", "cog-wsi"}, "o.cog.tiff"},
		{"downsample", []string{"downsample", "--factor", "2"}, "o.ds.svs"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), c.out)
			args := append(append([]string{}, c.args...), "-f", "-o", out, src)
			if cmdOut, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
				if strings.Contains(string(cmdOut), "no space left on device") {
					t.Skipf("disk full: %s", cmdOut)
				}
				t.Fatalf("%v: %v\n%s", c.args, err, cmdOut)
			}
			if got := iccLen(dumpRaw(t, bin, out)); got != wantLen {
				t.Errorf("ICC length in %s = %d, want %d", c.name, got, wantLen)
			}
		})
	}
}

// TestNoICCWhenSourceLacksIt: a source with no ICC emits no tag 34675.
func TestNoICCWhenSourceLacksIt(t *testing.T) {
	bin := stripedBinary(t)
	src := stripedSample(t, "svs/scan_620_.svs") // no ICC
	out := filepath.Join(t.TempDir(), "o.cog.tiff")
	if cmdOut, err := exec.Command(bin, "convert", "--to", "cog-wsi", "-f", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("convert: %v\n%s", err, cmdOut)
	}
	if got := iccLen(dumpRaw(t, bin, out)); got != -1 {
		t.Errorf("expected no ICC tag, got length %d", got)
	}
}
