package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/wsilabs/wsitools/internal/codec/all"
	_ "github.com/wsilabs/opentile-go/decoder/all"
	"github.com/spf13/cobra"
)

var (
	flagQuiet     bool
	flagVerbose   bool
	flagLogLevel  string
	flagLogFormat string
)

var rootCmd = &cobra.Command{
	Use:   "wsitools",
	Short: "Utilities for whole-slide imaging (WSI) files",
	Long: `wsitools — a Swiss-army knife for whole-slide imaging files used in digital pathology.

Run 'wsitools <command> --help' for command-specific flags and examples.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return setupLogger()
	},
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&flagQuiet, "quiet", false, "suppress progress bar")
	rootCmd.PersistentFlags().BoolVar(&flagVerbose, "verbose", false, "enable per-level summaries on stderr")
	rootCmd.PersistentFlags().StringVar(&flagLogLevel, "log-level", "info", "debug|info|warn|error")
	rootCmd.PersistentFlags().StringVar(&flagLogFormat, "log-format", "text", "text|json")
}

func setupLogger() error {
	var level slog.Level
	switch flagLogLevel {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return fmt.Errorf("invalid --log-level %q", flagLogLevel)
	}
	var handler slog.Handler
	opts := &slog.HandlerOptions{Level: level}
	switch flagLogFormat {
	case "text":
		handler = slog.NewTextHandler(os.Stderr, opts)
	case "json":
		handler = slog.NewJSONHandler(os.Stderr, opts)
	default:
		return fmt.Errorf("invalid --log-format %q", flagLogFormat)
	}
	slog.SetDefault(slog.New(handler))
	return nil
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	rootCmd.SetContext(ctx)
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "interrupted")
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
