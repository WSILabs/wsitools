package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

const Version = "0.26.6"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print wsitools version + build info",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("wsitools %s\n", Version)
		fmt.Printf("go %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return nil
	},
}

func init() { rootCmd.AddCommand(versionCmd) }
