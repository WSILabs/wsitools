package main

import (
	"strings"
	"testing"
)

const sampleDesc = `Aperio Image Library v12.0.15
46000x32914 [0,100 46000x32814] (240x240) JPEG/RGB Q=70|Aperio Image Library v12.0.15
46000x32914 -> 11500x8228 - |AppMag = 40|StripeWidth = 992|ScanScope ID = SS1234|Filename = test|Date = 03/12/19|Time = 13:14:15|MPP = 0.2497|Left = 25.691574|Top = 23.449873|LineCameraSkew = -0.000424|LineAreaXOffset = 0.019265|LineAreaYOffset = -0.000313|Focus Offset = 0.000000|ImageID = 1234|OriginalWidth = 46000|OriginalHeight = 32914|ICC Profile = ScanScope v1`

func TestParseImageDescription(t *testing.T) {
	d, err := ParseImageDescription(sampleDesc)
	if err != nil {
		t.Fatalf("ParseImageDescription: %v", err)
	}
	if d.AppMag != 40 {
		t.Errorf("AppMag: got %v, want 40", d.AppMag)
	}
	if d.MPP != 0.2497 {
		t.Errorf("MPP: got %v, want 0.2497", d.MPP)
	}
	if d.SoftwareLine != "Aperio Image Library v12.0.15" {
		t.Errorf("SoftwareLine: got %q", d.SoftwareLine)
	}
}

func TestMutateForDownsample_Factor2(t *testing.T) {
	d, _ := ParseImageDescription(sampleDesc)
	d.MutateForDownsample(2, 23000, 16457) // new W/H = source/2
	out := d.Encode()
	if !strings.Contains(out, "AppMag = 20") {
		t.Errorf("expected AppMag=20 in:\n%s", out)
	}
	if !strings.Contains(out, "MPP = 0.4994") {
		t.Errorf("expected MPP=0.4994 in:\n%s", out)
	}
	if !strings.Contains(out, "23000x16457") {
		t.Errorf("expected 23000x16457 in:\n%s", out)
	}
}
