package szi

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"

	"github.com/wsilabs/wsitools/internal/dzi"
	"github.com/wsilabs/wsitools/internal/source"
)

// Config mirrors dzi.Config; Name doubles as the top-level
// directory inside the ZIP.
type Config struct {
	Name     string
	Width    int
	Height   int
	Format   string // "jpeg" or "png"
	TileSize int
	Overlap  int
}

// Writer wraps a dzi.Writer with a zip.Writer using Store method.
// All entries land under "<Name>/" inside the archive.
type Writer struct {
	zw       *zip.Writer
	dziW     *dzi.Writer
	cfg      Config
	closed   bool
	scanXML  []byte // pending scan-properties.xml; emitted at Close
}

// NewWriter constructs an SZI writer that streams a ZIP archive to w.
func NewWriter(w io.Writer, cfg Config) (*Writer, error) {
	zw := zip.NewWriter(w)
	fs := &zipFS{zw: zw, prefix: cfg.Name + "/"}
	dziW, err := dzi.NewWriter(fs, dzi.Config{
		Name: cfg.Name, Width: cfg.Width, Height: cfg.Height,
		Format: cfg.Format, TileSize: cfg.TileSize, Overlap: cfg.Overlap,
	})
	if err != nil {
		return nil, err
	}
	return &Writer{zw: zw, dziW: dziW, cfg: cfg}, nil
}

// WriteTile delegates to the inner DZI writer.
func (w *Writer) WriteTile(level, col, row int, body []byte) error {
	return w.dziW.WriteTile(level, col, row, body)
}

// WriteScanProperties buffers the scan-properties.xml document; it is
// emitted into the ZIP at Close so it appears after the tile tree.
func (w *Writer) WriteScanProperties(md source.Metadata) error {
	var buf bytes.Buffer
	if err := WriteScanProperties(&buf, md); err != nil {
		return err
	}
	w.scanXML = buf.Bytes()
	return nil
}

// WriteAssociated stores an associated image PNG at
// <Name>/<Name>_associated/<typ>.png inside the archive, delegating to the inner
// DZI writer (whose WriteFS is this archive). Must be called before Close.
func (w *Writer) WriteAssociated(typ string, pngBytes []byte) error {
	return w.dziW.WriteAssociated(typ, pngBytes)
}

// Close flushes the DZI manifest, writes scan-properties.xml if present,
// and closes the ZIP central directory.
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	if err := w.dziW.Close(); err != nil {
		return err
	}
	if w.scanXML != nil {
		hdr := &zip.FileHeader{Name: w.cfg.Name + "/scan-properties.xml", Method: zip.Store}
		f, err := w.zw.CreateHeader(hdr)
		if err != nil {
			return fmt.Errorf("szi: create scan-properties: %w", err)
		}
		if _, err := f.Write(w.scanXML); err != nil {
			return fmt.Errorf("szi: write scan-properties: %w", err)
		}
	}
	return w.zw.Close()
}

// zipFS adapts archive/zip.Writer to dzi.WriteFS using Store method.
type zipFS struct {
	zw     *zip.Writer
	prefix string
}

func (fs *zipFS) Create(path string) (io.WriteCloser, error) {
	hdr := &zip.FileHeader{Name: fs.prefix + path, Method: zip.Store}
	w, err := fs.zw.CreateHeader(hdr)
	if err != nil {
		return nil, err
	}
	return nopCloser{w}, nil
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }
