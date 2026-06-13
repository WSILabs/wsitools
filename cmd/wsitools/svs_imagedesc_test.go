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

const cropTestOrigDesc = "Aperio Image Library v10.0.51\r\n" +
	"79560x30562 [0,100 78000x30462] (256x256) JPEG/RGB Q=30|AppMag = 20|MPP = 0.4990|" +
	"Left = 27.409658|Top = 20.522137|ImageID = 1004487|OriginalWidth = 79560|Originalheight = 30562"

func TestAperioDescription_Quality(t *testing.T) {
	d, err := ParseImageDescription(cropTestOrigDesc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	q, ok := d.Quality()
	if !ok || q != 30 {
		t.Fatalf("Quality() = %d,%v want 30,true", q, ok)
	}
}

func TestBuildCropImageDescription(t *testing.T) {
	got := BuildCropImageDescription(cropTestOrigDesc, 78000, 30462, 46492, 3599, 27836, 25633, 256, 256, 30)

	wantGeo := "78000x30462 [46492,3599 27836x25633] (256x256) JPEG/RGB Q=30;"
	if !strings.Contains(got, wantGeo) {
		t.Errorf("missing geometry line %q in:\n%s", wantGeo, got)
	}
	if !strings.Contains(got, "Aperio Image Library v10.0.51") {
		t.Errorf("provenance chain missing original software line")
	}
	if !strings.HasPrefix(got, "Aperio") {
		t.Errorf("crop description must start with Aperio")
	}
	if !strings.Contains(got, "OriginalWidth = 78000") || !strings.Contains(got, "OriginalHeight = 30462") {
		t.Errorf("missing appended OriginalWidth/Height = base dims:\n%s", got)
	}
	for _, f := range []string{"MPP = 0.4990", "AppMag = 20", "ImageID = 1004487", "Left = 27.409658"} {
		if !strings.Contains(got, f) {
			t.Errorf("missing preserved field %q", f)
		}
	}
}
