package main

import "testing"

func TestTranscodeRegistered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"transcode"})
	if err != nil || cmd.Name() != "transcode" {
		t.Fatalf("transcode command not found: %v", err)
	}
	if cmd.Flags().Lookup("codec") == nil {
		t.Fatal("transcode missing --codec flag")
	}
	if cmd.Flags().Lookup("rect") != nil {
		t.Fatal("transcode must NOT expose --rect (single-axis: codec only)")
	}
	if cmd.Flags().Lookup("to") != nil {
		t.Fatal("transcode must NOT expose --to (format-preserving)")
	}
}
