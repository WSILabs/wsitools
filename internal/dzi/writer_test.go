package dzi

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
)

// memFS implements WriteFS in memory for tests.
type memFS struct {
	mu    sync.Mutex
	files map[string][]byte
}

func newMemFS() *memFS { return &memFS{files: map[string][]byte{}} }

type memFile struct {
	fs   *memFS
	path string
	buf  bytes.Buffer
}

func (f *memFile) Write(p []byte) (int, error) { return f.buf.Write(p) }
func (f *memFile) Close() error {
	f.fs.mu.Lock()
	defer f.fs.mu.Unlock()
	if _, dup := f.fs.files[f.path]; dup {
		return fmt.Errorf("duplicate path %q", f.path)
	}
	f.fs.files[f.path] = append([]byte(nil), f.buf.Bytes()...)
	return nil
}

func (fs *memFS) Create(path string) (io.WriteCloser, error) {
	return &memFile{fs: fs, path: path}, nil
}

func (fs *memFS) paths() []string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	out := make([]string, 0, len(fs.files))
	for k := range fs.files {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestWriterEmitsManifestAndTiles(t *testing.T) {
	fs := newMemFS()
	w, err := NewWriter(fs, Config{
		Name: "cmu1", Width: 2220, Height: 2967,
		Format: "jpeg", TileSize: 256, Overlap: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Write every tile of every level.
	max := MaxLevel(2220, 2967)
	for lvl := 0; lvl <= max; lvl++ {
		lw, lh := LevelDims(2220, 2967, lvl)
		cols, rows := GridDims(lw, lh, 256)
		for col := 0; col < cols; col++ {
			for row := 0; row < rows; row++ {
				if err := w.WriteTile(lvl, col, row, []byte("FAKEJPEG")); err != nil {
					t.Fatalf("WriteTile L%d (%d,%d): %v", lvl, col, row, err)
				}
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	manifest, ok := fs.files["cmu1.dzi"]
	if !ok {
		t.Fatal("manifest cmu1.dzi missing")
	}
	if !strings.Contains(string(manifest), `Width="2220"`) {
		t.Errorf("manifest missing Width attr")
	}
	if _, ok := fs.files["cmu1_files/0/0_0.jpeg"]; !ok {
		t.Errorf("L0/0_0.jpeg missing; got paths: %v", fs.paths())
	}
	corner := fmt.Sprintf("cmu1_files/%d/8_11.jpeg", max)
	if _, ok := fs.files[corner]; !ok {
		t.Errorf("%s missing", corner)
	}
}

func TestWriterDuplicateTileFails(t *testing.T) {
	fs := newMemFS()
	w, _ := NewWriter(fs, Config{
		Name: "x", Width: 256, Height: 256,
		Format: "jpeg", TileSize: 256, Overlap: 0,
	})
	max := MaxLevel(256, 256)
	if err := w.WriteTile(max, 0, 0, []byte("a")); err != nil {
		t.Fatal(err)
	}
	err := w.WriteTile(max, 0, 0, []byte("b"))
	if !errors.Is(err, ErrTileAlreadyWritten) {
		t.Errorf("got %v, want ErrTileAlreadyWritten", err)
	}
}
