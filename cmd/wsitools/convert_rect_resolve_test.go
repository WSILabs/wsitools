package main

import (
	"testing"

	"github.com/spf13/cobra"
	opentile "github.com/wsilabs/opentile-go"
)

func TestResolveConvertRect_NoFlags(t *testing.T) {
	cmd := &cobra.Command{}
	registerRectFlags(cmd)
	got, err := resolveConvertRect(cmd, 2220, 2967)
	if err != nil {
		t.Fatal(err)
	}
	want := opentile.Region{Origin: opentile.Point{X: 0, Y: 0}, Size: opentile.Size{W: 2220, H: 2967}}
	if got != want {
		t.Fatalf("no rect flags must be full L0: got %+v want %+v", got, want)
	}
}

func TestResolveConvertRect_Rect(t *testing.T) {
	cmd := &cobra.Command{}
	registerRectFlags(cmd)
	_ = cmd.Flags().Set("rect", "10,20,100,200")
	got, err := resolveConvertRect(cmd, 2220, 2967)
	if err != nil {
		t.Fatal(err)
	}
	want := opentile.Region{Origin: opentile.Point{X: 10, Y: 20}, Size: opentile.Size{W: 100, H: 200}}
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestResolveConvertRect_OutOfBounds(t *testing.T) {
	cmd := &cobra.Command{}
	registerRectFlags(cmd)
	_ = cmd.Flags().Set("rect", "0,0,9999,9999")
	if _, err := resolveConvertRect(cmd, 2220, 2967); err == nil {
		t.Fatal("expected out-of-bounds error")
	}
}
