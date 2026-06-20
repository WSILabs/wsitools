package main

import "testing"

func TestConvertHasAllowNonconformantFlag(t *testing.T) {
	if convertCmd.Flags().Lookup("allow-nonconformant") == nil {
		t.Fatal("convert missing --allow-nonconformant flag")
	}
	if transcodeCmd.Flags().Lookup("allow-nonconformant") == nil {
		t.Fatal("transcode missing --allow-nonconformant flag")
	}
}
