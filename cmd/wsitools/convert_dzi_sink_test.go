package main

import (
	"bytes"
	"fmt"
	stdjpeg "image/jpeg"
	"image/png"
	"testing"
)

func sprintfLevel(level, col, row int) string { return fmt.Sprintf("%d:%d,%d", level, col, row) }

// recordingDZISink captures (level,col,row) routed through the adapter.
type recordingDZISink struct{ writes []string }

func (s *recordingDZISink) WriteTile(level, col, row int, body []byte) error {
	s.writes = append(s.writes, sprintfLevel(level, col, row))
	return nil
}

func TestDZIWriterSinkMapsEngineLevelToDZINumber(t *testing.T) {
	rec := &recordingDZISink{}
	// 10 engine levels (k=0 finest .. k=9 coarsest). Engine k=0 → DZI 9; k=9 → DZI 0.
	sink := newDZIWriterSink(rec, 10)
	if err := sink.WriteTile(0, 1, 2, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := sink.WriteTile(9, 0, 0, []byte("y")); err != nil {
		t.Fatal(err)
	}
	if rec.writes[0] != sprintfLevel(9, 1, 2) {
		t.Errorf("engine k=0 → %q, want DZI level 9", rec.writes[0])
	}
	if rec.writes[1] != sprintfLevel(0, 0, 0) {
		t.Errorf("engine k=9 → %q, want DZI level 0", rec.writes[1])
	}
}

func TestDZIStandaloneEncoderJPEGIsSelfContained(t *testing.T) {
	enc, err := newDZIStandaloneEncoder("jpeg", 256, 85)
	if err != nil {
		t.Fatalf("new encoder: %v", err)
	}
	defer enc.Close()
	rgb := make([]byte, 16*16*3)
	for i := range rgb {
		rgb[i] = 128
	}
	body, err := enc.EncodeTile(rgb, 16, 16)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := stdjpeg.Decode(bytes.NewReader(body)); err != nil {
		t.Errorf("DZI JPEG must be self-contained (stdlib-decodable): %v", err)
	}
}

func TestDZIStandaloneEncoderPNG(t *testing.T) {
	enc, err := newDZIStandaloneEncoder("png", 8, 0)
	if err != nil {
		t.Fatalf("new encoder: %v", err)
	}
	defer enc.Close()
	rgb := make([]byte, 8*8*3)
	body, err := enc.EncodeTile(rgb, 8, 8)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("png decode: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 8 || b.Dy() != 8 {
		t.Errorf("png dims = %v, want 8×8", b)
	}
}
