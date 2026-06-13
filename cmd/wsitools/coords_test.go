package main

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestResolveRectValues_MutualExclusion(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	var rect string
	var x, y, w, h int
	cmd.Flags().StringVar(&rect, "rect", "", "")
	cmd.Flags().IntVar(&x, "x", 0, "")
	cmd.Flags().IntVar(&y, "y", 0, "")
	cmd.Flags().IntVar(&w, "w", 0, "")
	cmd.Flags().IntVar(&h, "h", 0, "")
	cmd.SetArgs([]string{"--rect", "1,2,3,4", "--x", "5"})
	if err := cmd.ParseFlags([]string{"--rect", "1,2,3,4", "--x", "5"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, _, _, _, err := resolveRectValues(cmd, rect, x, y, w, h); err == nil {
		t.Error("expected mutual-exclusion error")
	}
}
