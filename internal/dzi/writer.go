package dzi

import (
	"errors"
	"fmt"
	"io"
	"sync"
)

// WriteFS abstracts the destination filesystem. The DZI target backs
// this with os.MkdirAll + os.Create; the SZI target backs it with an
// archive/zip writer.
type WriteFS interface {
	// Create returns a WriteCloser for path. Implementations are
	// responsible for any directory creation implied by path.
	Create(path string) (io.WriteCloser, error)
}

// Config captures the manifest-level invariants the writer needs.
type Config struct {
	Name     string
	Width    int
	Height   int
	Format   string // "jpeg" or "png"
	TileSize int
	Overlap  int
}

// ErrTileAlreadyWritten is returned by WriteTile if the same
// (level, col, row) coordinate is written twice.
var ErrTileAlreadyWritten = errors.New("dzi: tile already written")

// Writer emits DZI manifest + tile tree to a WriteFS. Concurrent
// WriteTile calls are safe.
type Writer struct {
	fs   WriteFS
	cfg  Config
	mu   sync.Mutex
	seen map[uint64]struct{}
}

func NewWriter(fs WriteFS, cfg Config) (*Writer, error) {
	if cfg.Name == "" {
		return nil, fmt.Errorf("dzi: empty Name")
	}
	if cfg.Format != "jpeg" && cfg.Format != "png" {
		return nil, fmt.Errorf("dzi: Format must be jpeg or png, got %q", cfg.Format)
	}
	if cfg.TileSize <= 0 {
		return nil, fmt.Errorf("dzi: TileSize must be positive, got %d", cfg.TileSize)
	}
	if cfg.Overlap < 0 {
		return nil, fmt.Errorf("dzi: Overlap must be non-negative, got %d", cfg.Overlap)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, fmt.Errorf("dzi: Width/Height must be positive, got %dx%d", cfg.Width, cfg.Height)
	}
	return &Writer{fs: fs, cfg: cfg, seen: map[uint64]struct{}{}}, nil
}

func tileKey(level, col, row int) uint64 {
	return (uint64(level) << 40) | (uint64(col) << 20) | uint64(row)
}

// WriteTile writes the compressed tile bytes for (level, col, row).
// Path: <Name>_files/<level>/<col>_<row>.<ext>.
func (w *Writer) WriteTile(level, col, row int, body []byte) error {
	k := tileKey(level, col, row)
	w.mu.Lock()
	if _, dup := w.seen[k]; dup {
		w.mu.Unlock()
		return fmt.Errorf("%w: level=%d col=%d row=%d", ErrTileAlreadyWritten, level, col, row)
	}
	w.seen[k] = struct{}{}
	w.mu.Unlock()

	path := fmt.Sprintf("%s_files/%d/%d_%d.%s", w.cfg.Name, level, col, row, w.cfg.Format)
	f, err := w.fs.Create(path)
	if err != nil {
		return fmt.Errorf("dzi: create %s: %w", path, err)
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		return fmt.Errorf("dzi: write %s: %w", path, err)
	}
	return f.Close()
}

// Close emits the manifest. Tile writes must complete before Close.
func (w *Writer) Close() error {
	path := w.cfg.Name + ".dzi"
	f, err := w.fs.Create(path)
	if err != nil {
		return fmt.Errorf("dzi: create %s: %w", path, err)
	}
	m := Manifest{
		Format:   w.cfg.Format,
		Overlap:  w.cfg.Overlap,
		TileSize: w.cfg.TileSize,
		Width:    w.cfg.Width,
		Height:   w.cfg.Height,
	}
	if err := m.WriteTo(f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
