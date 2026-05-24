package cogwsiwriter_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wsilabs/wsitools/internal/tiff/cogwsiwriter"
	"github.com/wsilabs/wsitools/internal/tiff/tileorder"
)

func TestCOGWSIWriterRejectsUnsupportedOrder(t *testing.T) {
	dir := t.TempDir()
	dummyPath := filepath.Join(dir, "out.tif")
	bad := badOrder{}
	opts := cogwsiwriter.Options{DefaultOrder: bad}
	_, err := cogwsiwriter.Create(dummyPath, opts)
	if err == nil {
		_ = os.Remove(dummyPath)
		t.Fatalf("Create with bad order: expected error, got nil")
	}
}

type badOrder struct{}

func (badOrder) Name() string                              { return "no-such-order" }
func (badOrder) Index(_, _, _, _ uint32) uint32            { return 0 }
func (badOrder) IndexToXY(_, _, _ uint32) (uint32, uint32) { return 0, 0 }

var _ tileorder.OrderStrategy = badOrder{}
