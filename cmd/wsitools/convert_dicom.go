package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"
	opentile "github.com/wsilabs/opentile-go"

	"github.com/wsilabs/wsitools/internal/derivedsource"
	"github.com/wsilabs/wsitools/internal/dicomwriter"
	"github.com/wsilabs/wsitools/internal/source"
)

var cvDICOMLevel int

func init() {
	convertCmd.Flags().IntVar(&cvDICOMLevel, "level", 0, "emit only this pyramid level (--to dicom; omit for the full pyramid)")
}

// runConvertDICOM emits DICOM WSM VOLUME instance(s) from a DICOM or non-DICOM
// JPEG-baseline source. Without --level it emits the full pyramid (one instance
// per level) into the -o directory as level-<n>.dcm; with --level it emits one
// instance to the -o file.
func runConvertDICOM(cmd *cobra.Command, input string, start time.Time) error {
	if cvOutput == "" {
		return fmt.Errorf("-o/--output is required")
	}
	if _, err := os.Stat(input); err != nil {
		return fmt.Errorf("input %s: %w", input, err)
	}
	if !cvForce {
		if _, err := os.Stat(cvOutput); err == nil {
			return fmt.Errorf("output %s already exists (use --force)", cvOutput)
		}
	}
	src, slide, err := source.OpenWithSlide(input)
	if err != nil {
		if errors.Is(err, source.ErrUnsupportedFormat) {
			return fmt.Errorf("source format unsupported: %w", err)
		}
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	// --tile-size forces a re-tile, which the verbatim/1:1 frame path can't do.
	// Route through the engine at full resolution (factor 1) so frames are
	// re-tiled to the requested size (Rows/Columns follow). The engine re-encodes
	// to JPEG-baseline.
	srcL0 := slide.Levels()[0]
	if cvTileSize > 0 && cvTileSize != srcL0.TileSize.W {
		if cvCodec != "" && cvCodec != "jpeg" {
			return fmt.Errorf("--codec %q is not supported for --to dicom (only 'jpeg')", cvCodec)
		}
		if cmd.Flags().Changed("level") {
			return fmt.Errorf("--tile-size with --level is not supported for --to dicom; omit one")
		}
		quality, qerr := dicomReencodeQuality()
		if qerr != nil {
			return qerr
		}
		workers := cvWorkers
		if workers == 0 {
			workers = runtime.NumCPU()
		}
		slog.Warn("re-tiling pyramid to JPEG-baseline (lossy) for --to dicom --tile-size", "tile", cvTileSize, "quality", quality)
		md := src.Metadata()
		assoc := src.Associated()
		if cvNoAssociated {
			assoc = nil
		}
		srcRegion := opentile.Region{Origin: opentile.Point{X: 0, Y: 0}, Size: srcL0.Size}
		return runDICOMEngine(cmd.Context(), slide, srcRegion, opentile.Size{W: srcL0.Size.W, H: srcL0.Size.H},
			"jpeg", quality, workers, src.Format(), md, assoc,
			dicomwriter.Options{Associated: !cvNoAssociated, L0ImageType: []string{"DERIVED", "PRIMARY", "VOLUME", "NONE"}},
			cvOutput, cvForce)
	}

	// --codec is the explicit opt-in to RE-ENCODE the pyramid (lossy) rather
	// than frame-copy it verbatim — mirroring the TIFF family's --codec. Without
	// it, a source whose tiles are not a DICOM transfer syntax is rejected by the
	// writer (no silent codec assumptions). JPEG-baseline is the only re-encode
	// target available for DICOM today (no JPEG 2000 / HTJ2K encoder).
	emit := src
	if cvCodec != "" {
		if cvCodec != "jpeg" {
			return fmt.Errorf("--codec %q is not supported for --to dicom (only 'jpeg'; omit --codec to frame-copy a JPEG/JPEG2000/HTJ2K/JPEG XL source verbatim)", cvCodec)
		}
		quality, qerr := dicomReencodeQuality()
		if qerr != nil {
			return qerr
		}
		workers := cvWorkers
		if workers == 0 {
			workers = runtime.NumCPU()
		}
		slog.Warn("re-encoding pyramid to JPEG-baseline (lossy) for --to dicom --codec jpeg",
			"quality", quality)
		emit = derivedsource.TranscodeToJPEG(src, quality, workers)
	}

	if cmd.Flags().Changed("level") {
		return writeDICOMSingle(emit, start)
	}
	return writeDICOMPyramid(emit, start)
}

// dicomReencodeQuality parses the --quality flag (default 90) for the
// --to dicom --codec jpeg re-encode path.
func dicomReencodeQuality() (int, error) {
	quality := 90
	if cvQuality != "" {
		if _, err := fmt.Sscanf(cvQuality, "%d", &quality); err != nil {
			return 0, fmt.Errorf("--quality %q: must be an integer 1..100", cvQuality)
		}
	}
	if quality < 1 || quality > 100 {
		return 0, fmt.Errorf("--quality must be 1..100")
	}
	return quality, nil
}

// writeDICOMSingle emits one WSM instance for cvDICOMLevel to the cvOutput file.
func writeDICOMSingle(src source.Source, start time.Time) error {
	f, err := os.Create(cvOutput)
	if err != nil {
		return fmt.Errorf("create %s: %w", cvOutput, err)
	}
	if err := dicomwriter.WriteVolumeInstance(f, src, cvDICOMLevel, dicomwriter.Options{}); err != nil {
		f.Close()
		_ = os.Remove(cvOutput)
		return fmt.Errorf("write DICOM instance: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", cvOutput, err)
	}
	if stat, _ := os.Stat(cvOutput); stat != nil {
		slog.Info("convert complete",
			"output", cvOutput, "size", formatBytes(stat.Size()),
			"level", cvDICOMLevel, "elapsed", time.Since(start).Round(time.Millisecond))
		fmt.Printf("wrote %s (%s, %s)\n", cvOutput, formatBytes(stat.Size()), time.Since(start).Round(time.Millisecond))
	}
	return nil
}

// emitDICOM writes a full DICOM-WSM pyramid for src into outDir (one
// level-<n>.dcm per instance, plus associated images). It writes into a temp
// sibling dir and renames into place so a failed run never leaves a partial
// pyramid. Shared by writeDICOMPyramid (convert --to dicom) and the
// downsample/crop DICOM emitters.
func emitDICOM(src source.Source, opts dicomwriter.Options, outDir string, force bool) error {
	parent := filepath.Dir(outDir)
	tmp, err := os.MkdirTemp(parent, ".wsitools-dcm-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	factory := func(name string) (io.WriteCloser, error) {
		return os.Create(filepath.Join(tmp, name+".dcm"))
	}
	if err := dicomwriter.WritePyramid(src, opts, factory); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("write DICOM pyramid: %w", err)
	}
	if force {
		if err := os.RemoveAll(outDir); err != nil {
			_ = os.RemoveAll(tmp)
			return fmt.Errorf("remove existing %s: %w", outDir, err)
		}
	}
	if err := os.Rename(tmp, outDir); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("finalize %s: %w", outDir, err)
	}
	return nil
}

// writeDICOMPyramid emits the full pyramid into cvOutput (a directory) as
// level-<n>.dcm. It writes into a temp sibling dir and renames into place so a
// failed run never leaves a partial pyramid.
func writeDICOMPyramid(src source.Source, start time.Time) error {
	if err := emitDICOM(src, dicomwriter.Options{Associated: !cvNoAssociated}, cvOutput, cvForce); err != nil {
		return err
	}
	entries, _ := os.ReadDir(cvOutput)
	n := 0
	var total int64
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".dcm" {
			n++
			if info, err := e.Info(); err == nil {
				total += info.Size()
			}
		}
	}
	slog.Info("convert complete",
		"output", cvOutput, "instances", n, "size", formatBytes(total),
		"elapsed", time.Since(start).Round(time.Millisecond))
	fmt.Printf("wrote %s (%d instances, %s, %s)\n", cvOutput, n, formatBytes(total), time.Since(start).Round(time.Millisecond))
	return nil
}
