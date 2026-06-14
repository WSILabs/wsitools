package dicomwriter

import (
	"bytes"
	"errors"
	"image"
	"io"
	"testing"

	opentile "github.com/wsilabs/opentile-go"
	"github.com/wsilabs/opentile-go/decoder"
	"github.com/wsilabs/wsitools/internal/source"
)

// skipAssoc is an associated image whose Bytes() fails, so writeAssociated
// returns errSkipAssociated (the skip path).
type skipAssoc struct{}

func (skipAssoc) Type() string                     { return "label" }
func (skipAssoc) Size() image.Point                { return image.Point{X: 100, Y: 100} }
func (skipAssoc) Compression() source.Compression  { return source.CompressionJPEG }
func (skipAssoc) Bytes() ([]byte, error)           { return nil, errors.New("boom: cannot read bytes") }
func (skipAssoc) Decode(decoder.DecodeOptions) (*decoder.Image, error) {
	return nil, errors.New("not called")
}
func (skipAssoc) Source() (opentile.AssociatedEncoding, bool) {
	return opentile.AssociatedEncoding{}, false
}
func (skipAssoc) IFDOffset() (int64, bool) { return 0, false }

// skipSource has no pyramid levels (the level loop is a no-op) and one
// associated image that always skips.
type skipSource struct{}

func (skipSource) Format() string                       { return "svs" }
func (skipSource) Levels() []source.Level               { return nil }
func (skipSource) Associated() []source.AssociatedImage { return []source.AssociatedImage{skipAssoc{}} }
func (skipSource) Metadata() source.Metadata            { return source.Metadata{} }
func (skipSource) SourceImageDescription() string       { return "" }
func (skipSource) Close() error                         { return nil }

// TestWritePyramid_SkipAssociatedLeavesNoFile guards the stray-0-byte-file bug:
// a skipped associated image must NOT cause an output file to be created.
func TestWritePyramid_SkipAssociatedLeavesNoFile(t *testing.T) {
	var created []string
	newWriter := func(name string) (io.WriteCloser, error) {
		created = append(created, name)
		return nopWriteCloser{new(bytes.Buffer)}, nil
	}
	if err := WritePyramid(skipSource{}, Options{Associated: true}, newWriter); err != nil {
		t.Fatalf("WritePyramid: %v", err)
	}
	if len(created) != 0 {
		t.Errorf("skipped associated created %d file(s) %v; want 0 (stray 0-byte file)", len(created), created)
	}
}
