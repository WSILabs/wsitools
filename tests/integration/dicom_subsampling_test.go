//go:build integration

package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// dicomL0Chroma runs `info --json` on a written slide (file or DICOM dir) and
// returns level 0's effective chroma subsampling ("4:4:4" / "4:2:2" / "4:2:0" /
// "" ). Used to assert a DICOM re-encode honors the source's chroma instead of
// forcing the encoder default (4:2:0). (wsitools DICOM subsampling gap)
func dicomL0Chroma(t *testing.T, bin, path string) string {
	t.Helper()
	out, err := exec.Command(bin, "info", "--json", path).Output()
	if err != nil {
		t.Fatalf("info --json %s: %v", path, err)
	}
	var res struct {
		Levels []struct {
			Quality *struct {
				ChromaSubsampling string `json:"chroma_subsampling"`
			} `json:"quality"`
		} `json:"levels"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal info json: %v\n%s", err, out)
	}
	if len(res.Levels) == 0 || res.Levels[0].Quality == nil {
		t.Fatalf("no L0 quality in info json:\n%s", out)
	}
	return res.Levels[0].Quality.ChromaSubsampling
}

// TestConvertDICOM_HonorsSourceSubsampling guards the DICOM re-encode chroma gap:
// re-encoding a JPEG source to DICOM must match the source's chroma subsampling
// (4:2:2 stays 4:2:2, 4:4:4 stays 4:4:4) instead of silently downgrading to the
// JPEG encoder default 4:2:0 — the same honor-source behavior the TIFF family
// already has. Covers BOTH re-encode paths: the engine (--factor) and the 1:1
// transcode (--codec jpeg). Seeds each source by re-encoding CMU with an explicit
// subsampling so the input chroma is known.
func TestConvertDICOM_HonorsSourceSubsampling(t *testing.T) {
	bin := buildOnce(t)
	base := filepath.Join(testdir(t), "svs", "CMU-1-Small-Region.svs")
	if _, err := os.Stat(base); err != nil {
		t.Skipf("no svs fixture")
	}
	dir := t.TempDir()

	for _, tc := range []struct {
		knob string // encoder subsampling knob for the source
		want string // expected chroma in info output
	}{
		{"422", "4:2:2"},
		{"444", "4:4:4"},
	} {
		// Build a source SVS with a known chroma subsampling.
		src := filepath.Join(dir, "src"+tc.knob+".svs")
		if o, err := exec.Command(bin, "convert", "--to", "svs", "--codec", "jpeg",
			"--quality", "subsampling="+tc.knob, "-f", "-o", src, base).CombinedOutput(); err != nil {
			t.Fatalf("build src %s: %v\n%s", tc.knob, err, o)
		}
		if got := dicomL0Chroma(t, bin, src); got != tc.want {
			t.Fatalf("source %s chroma = %q, want %q (setup failed)", tc.knob, got, tc.want)
		}

		// Engine re-encode path (--factor 2). Output L0 must keep the source chroma.
		engineOut := filepath.Join(dir, "engine"+tc.knob+".dcmdir")
		if o, err := exec.Command(bin, "convert", "--to", "dicom", "--factor", "2",
			"-f", "-o", engineOut, src).CombinedOutput(); err != nil {
			t.Fatalf("convert --to dicom --factor 2 (%s): %v\n%s", tc.knob, err, o)
		}
		if got := dicomL0Chroma(t, bin, engineOut); got != tc.want {
			t.Errorf("engine re-encode of %s source: DICOM L0 chroma = %q, want %q (forced 4:2:0?)", tc.knob, got, tc.want)
		}

		// 1:1 transcode path (--codec jpeg). Same expectation.
		transOut := filepath.Join(dir, "trans"+tc.knob+".dcmdir")
		if o, err := exec.Command(bin, "convert", "--to", "dicom", "--codec", "jpeg",
			"-f", "-o", transOut, src).CombinedOutput(); err != nil {
			t.Fatalf("convert --to dicom --codec jpeg (%s): %v\n%s", tc.knob, err, o)
		}
		if got := dicomL0Chroma(t, bin, transOut); got != tc.want {
			t.Errorf("transcode of %s source: DICOM L0 chroma = %q, want %q (forced 4:2:0?)", tc.knob, got, tc.want)
		}
	}
}
