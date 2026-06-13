package main

import (
	"testing"

	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff"
)

func findAssociated(t *testing.T, s source.Source, typ string) source.AssociatedImage {
	t.Helper()
	for _, a := range s.Associated() {
		if a.Type() == typ {
			return a
		}
	}
	t.Fatalf("associated image %q not found", typ)
	return nil
}

func TestFaithfulSpecsCMU(t *testing.T) {
	path := firstExisting(t, "svs/CMU-1-Small-Region.svs")
	if path == "" {
		t.Skip("CMU-1-Small-Region.svs fixture not available")
	}
	s, err := source.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	// label: LZW, faithful multi-strip source with Predictor==2.
	label := findAssociated(t, s, "label")
	cog, err := faithfulCOGWSISpec(label)
	if err != nil {
		t.Fatalf("faithfulCOGWSISpec(label): %v", err)
	}
	if len(cog.Strips) <= 1 {
		t.Errorf("label COG strips = %d, want > 1", len(cog.Strips))
	}
	if cog.Predictor != 2 {
		t.Errorf("label COG Predictor = %d, want 2", cog.Predictor)
	}
	if cog.Compression != tiff.CompressionLZW {
		t.Errorf("label COG Compression = %d, want CompressionLZW(%d)", cog.Compression, tiff.CompressionLZW)
	}

	strp, err := faithfulStrippedSpec(label)
	if err != nil {
		t.Fatalf("faithfulStrippedSpec(label): %v", err)
	}
	if len(strp.Strips) != len(cog.Strips) {
		t.Errorf("label stripped strips = %d, want %d (same as COG)", len(strp.Strips), len(cog.Strips))
	}
	if strp.Predictor != cog.Predictor {
		t.Errorf("label stripped Predictor = %d, want %d", strp.Predictor, cog.Predictor)
	}
	if strp.Compression != tiff.CompressionLZW {
		t.Errorf("label stripped Compression = %d, want CompressionLZW", strp.Compression)
	}

	// overview: JPEG, no predictor.
	overview := findAssociated(t, s, "overview")
	ocog, err := faithfulCOGWSISpec(overview)
	if err != nil {
		t.Fatalf("faithfulCOGWSISpec(overview): %v", err)
	}
	if ocog.Compression != tiff.CompressionJPEG {
		t.Errorf("overview COG Compression = %d, want CompressionJPEG(%d)", ocog.Compression, tiff.CompressionJPEG)
	}
	if ocog.Predictor > 1 {
		t.Errorf("overview COG Predictor = %d, want 0 or 1", ocog.Predictor)
	}
}
