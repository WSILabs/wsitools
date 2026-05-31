package main

import (
	"fmt"
	"sort"

	codec "github.com/wsilabs/wsitools/internal/codec"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Report installed codec libraries + version info",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("wsitools", Version, "— codec / library health check.")
		fmt.Println()
		fmt.Println("Codecs:")
		names := codec.List()
		sort.Strings(names)
		for _, name := range names {
			fmt.Printf("  ✓ %s\n", name)
		}
		fmt.Println()
		fmt.Println("Source decoders:")
		fmt.Println("  ✓ jpeg      (libjpeg-turbo via internal/decoder)")
		fmt.Println("  ✓ jpeg2000  (openjpeg via internal/decoder)")
		fmt.Println()
		fmt.Println("Reader: opentile-go (see go.mod for version)")
		fmt.Println()
		fmt.Println("Memory:")
		if memLimitResult.RAMBytes > 0 {
			fmt.Printf("  Physical RAM:  %s\n", formatBytes(int64(memLimitResult.RAMBytes)))
		} else {
			fmt.Println("  Physical RAM:  unknown")
		}
		fmt.Printf("  Soft limit:    %s  (source: %s)\n",
			memLimitDisplay(memLimitResult), memLimitResult.Source)
		return nil
	},
}

func init() { rootCmd.AddCommand(doctorCmd) }
