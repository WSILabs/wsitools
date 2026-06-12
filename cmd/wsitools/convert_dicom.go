package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

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
	src, err := source.Open(input)
	if err != nil {
		if errors.Is(err, source.ErrUnsupportedFormat) {
			return fmt.Errorf("source format unsupported: %w", err)
		}
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	if cmd.Flags().Changed("level") {
		return writeDICOMSingle(src, start)
	}
	return writeDICOMPyramid(src, start)
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

// writeDICOMPyramid emits the full pyramid into cvOutput (a directory) as
// level-<n>.dcm. It writes into a temp sibling dir and renames into place so a
// failed run never leaves a partial pyramid.
func writeDICOMPyramid(src source.Source, start time.Time) error {
	parent := filepath.Dir(cvOutput)
	tmp, err := os.MkdirTemp(parent, ".wsitools-dcm-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	factory := func(level int) (io.WriteCloser, error) {
		return os.Create(filepath.Join(tmp, fmt.Sprintf("level-%d.dcm", level)))
	}
	if err := dicomwriter.WritePyramid(src, dicomwriter.Options{}, factory); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("write DICOM pyramid: %w", err)
	}
	if cvForce {
		if err := os.RemoveAll(cvOutput); err != nil {
			_ = os.RemoveAll(tmp)
			return fmt.Errorf("remove existing %s: %w", cvOutput, err)
		}
	}
	if err := os.Rename(tmp, cvOutput); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("finalize %s: %w", cvOutput, err)
	}

	n := len(src.Levels())
	var total int64
	if entries, err := os.ReadDir(cvOutput); err == nil {
		for _, e := range entries {
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
