package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime/pprof"
	"syscall"

	_ "github.com/wsilabs/wsitools/internal/codec/all"
	"github.com/wsilabs/wsitools/internal/memlimit"
	_ "github.com/wsilabs/opentile-go/decoder/all"
	"github.com/spf13/cobra"
)

var (
	flagQuiet      bool
	flagVerbose    bool
	flagLogLevel   string
	flagLogFormat  string
	flagCPUProfile string
	flagMaxMemory  string

	cpuProfileFile *os.File

	memLimitResult memlimit.Result
)

var rootCmd = &cobra.Command{
	Use:   "wsitools",
	Short: "Utilities for whole-slide imaging (WSI) files",
	Long: `wsitools — a Swiss-army knife for whole-slide imaging files used in digital pathology.

Run 'wsitools <command> --help' for command-specific flags and examples.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if err := setupLogger(); err != nil {
			return err
		}
		res, applyErr := memlimit.Apply(flagMaxMemory)
		if applyErr != nil {
			return applyErr
		}
		memLimitResult = res
		slog.Debug("memory soft limit",
			"limit", memLimitDisplay(res),
			"source", res.Source,
			"ram", formatBytes(int64(res.RAMBytes)),
			"applied", res.Applied)
		if flagVerbose && res.Source != memlimit.SourceUnset {
			fmt.Fprintf(os.Stderr, "memory soft limit: %s (%s)\n", memLimitDisplay(res), res.Source)
		}
		if flagCPUProfile != "" {
			f, err := os.Create(flagCPUProfile)
			if err != nil {
				return fmt.Errorf("create cpu profile: %w", err)
			}
			cpuProfileFile = f
			if err := pprof.StartCPUProfile(f); err != nil {
				f.Close()
				return fmt.Errorf("start cpu profile: %w", err)
			}
		}
		return nil
	},
	PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
		if cpuProfileFile != nil {
			pprof.StopCPUProfile()
			cpuProfileFile.Close()
		}
		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&flagQuiet, "quiet", false, "suppress progress bar")
	rootCmd.PersistentFlags().BoolVar(&flagVerbose, "verbose", false, "enable per-level summaries on stderr")
	rootCmd.PersistentFlags().StringVar(&flagLogLevel, "log-level", "info", "debug|info|warn|error")
	rootCmd.PersistentFlags().StringVar(&flagLogFormat, "log-format", "text", "text|json")
	rootCmd.PersistentFlags().StringVar(&flagCPUProfile, "cpu-profile", "", "write CPU profile to <file> (debug)")
	rootCmd.PersistentFlags().StringVar(&flagMaxMemory, "max-memory", "",
		"soft memory cap, e.g. 8000 (MiB), 12GiB, or off; default 75% of RAM")
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
	defer func() {
		// Always finalise CPU profile on exit so SIGINT mid-run still writes data.
		if cpuProfileFile != nil {
			pprof.StopCPUProfile()
			cpuProfileFile.Close()
			cpuProfileFile = nil
		}
	}()
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

// memLimitDisplay renders a Result's limit for human output: the raw
// GOMEMLIMIT string on the env path, "none (unlimited)" for an uncapped
// limit, otherwise a human byte size.
func memLimitDisplay(r memlimit.Result) string {
	if r.Source == memlimit.SourceEnv {
		return r.RawEnv
	}
	if r.LimitBytes == memlimit.Unlimited {
		return "none (unlimited)"
	}
	return formatBytes(r.LimitBytes)
}
