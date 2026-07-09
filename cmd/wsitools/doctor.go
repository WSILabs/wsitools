package main

import (
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"
	codec "github.com/wsilabs/wsitools/internal/codec"
)

// probeTileSize is the dimension of the throwaway RGB tile doctor encodes to
// verify each codec can actually ENCODE — not merely that its library linked.
const probeTileSize = 64

// codecStatus is one codec's encode health. A codec that appears in the registry
// has its library linked; OK additionally reports whether a real encode
// succeeded. Detail carries the encoder's error when it did not — this is how a
// "library present but encoder unavailable" build (e.g. libavif with no AV1
// encoder backend on Windows) is distinguished from a fully working one.
type codecStatus struct {
	Name   string
	OK     bool
	Detail string
}

// probeCodecs runs a tiny real encode through every registered codec. A codec
// whose library linked but cannot encode (e.g. libavif built without aom/svt-av1/
// rav1e) surfaces as OK=false with the encoder's own reason.
func probeCodecs() []codecStatus {
	names := codec.List()
	sort.Strings(names)
	rgb := make([]byte, probeTileSize*probeTileSize*3)
	for i := range rgb {
		rgb[i] = 128 // mid-gray: real content, not an all-zero shortcut
	}
	out := make([]codecStatus, 0, len(names))
	for _, name := range names {
		st := codecStatus{Name: name, OK: true}
		if err := probeCodecEncode(name, rgb); err != nil {
			st.OK = false
			st.Detail = err.Error()
		}
		out = append(out, st)
	}
	return out
}

// probeCodecEncode encodes one probe tile through the named codec, returning the
// encoder's error if the encode fails.
func probeCodecEncode(name string, rgb []byte) error {
	fac, err := codec.Lookup(name)
	if err != nil {
		return err
	}
	enc, err := fac.NewEncoder(codec.LevelGeometry{
		TileWidth:   probeTileSize,
		TileHeight:  probeTileSize,
		PixelFormat: codec.PixelFormatRGB8,
	}, codec.Quality{})
	if err != nil {
		return err
	}
	defer enc.Close()
	if _, err := enc.EncodeTile(rgb, probeTileSize, probeTileSize, nil); err != nil {
		return err
	}
	return nil
}

// renderCodecHealth writes the "Codecs:" section: a check per codec that can
// encode, and an explicit "library present, encoder unavailable" line (with the
// reason) for one whose library linked but cannot encode.
func renderCodecHealth(w io.Writer, statuses []codecStatus) {
	fmt.Fprintln(w, "Codecs:")
	for _, st := range statuses {
		if st.OK {
			fmt.Fprintf(w, "  ✓ %s\n", st.Name)
		} else {
			fmt.Fprintf(w, "  ✗ %-9s library present, encoder unavailable: %s\n", st.Name, st.Detail)
		}
	}
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Report codec library health (verifies each codec can actually encode)",
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		fmt.Fprintln(out, "wsitools", Version, "— codec / library health check.")
		fmt.Fprintln(out)
		renderCodecHealth(out, probeCodecs())
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Source decoders:")
		fmt.Fprintln(out, "  ✓ jpeg      (libjpeg-turbo via internal/decoder)")
		fmt.Fprintln(out, "  ✓ jpeg2000  (openjpeg via internal/decoder)")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Reader: opentile-go (see go.mod for version)")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Memory:")
		if memLimitResult.RAMBytes > 0 {
			fmt.Fprintf(out, "  Physical RAM:  %s\n", formatBytes(int64(memLimitResult.RAMBytes)))
		} else {
			fmt.Fprintln(out, "  Physical RAM:  unknown")
		}
		fmt.Fprintf(out, "  Soft limit:    %s  (source: %s)\n",
			memLimitDisplay(memLimitResult), memLimitResult.Source)
		return nil
	},
}

func init() { rootCmd.AddCommand(doctorCmd) }
