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

func TestAperioCodecDescriptor(t *testing.T) {
	if aperioCodecDescriptor("jpeg") != "JPEG/RGB" || aperioCodecDescriptor("jpeg2000") != "J2K/YUV16" {
		t.Fatal("descriptor mapping wrong")
	}
	d := &AperioDescription{GeometryLine: "1000x1000 (256x256) JPEG/RGB Q=90"}
	setAperioCodecDescriptor(d, "jpeg2000")
	if !strings.Contains(d.GeometryLine, "J2K/YUV16") || strings.Contains(d.GeometryLine, "JPEG/RGB") {
		t.Fatalf("not rewritten: %q", d.GeometryLine)
	}
}

func TestBuildCropImageDescription(t *testing.T) {
	got := BuildCropImageDescription(cropTestOrigDesc, 78000, 30462, 46492, 3599, 27836, 25633, 256, 256, 30, "jpeg")

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

func TestScaleAperioResolutionTokens(t *testing.T) {
	in := "78000x30462 [0,0 78000x30462] (256x256) JPEG/RGB Q=30|AppMag = 20|MPP = 0.499|OriginalWidth = 78000"
	if got := scaleAperioResolutionTokens(in, 1); got != in {
		t.Fatalf("factor 1 must be identity:\n got %q", got)
	}
	got := scaleAperioResolutionTokens(in, 2)
	if !strings.Contains(got, "AppMag = 10") {
		t.Errorf("AppMag not halved: %q", got)
	}
	if !strings.Contains(got, "MPP = 0.998") {
		t.Errorf("MPP not doubled: %q", got)
	}
	if !strings.Contains(got, "78000x30462") || !strings.Contains(got, "OriginalWidth = 78000") {
		t.Errorf("pixel dims must be untouched: %q", got)
	}
}

func TestScaleAperioResolutionTokens_NoMPP(t *testing.T) {
	in := "27836x25633 [46492,3599 27836x25633] (256x256) JPEG/RGB Q=30|AppMag = 20|StripeWidth = 2040"
	got := scaleAperioResolutionTokens(in, 2)
	if !strings.Contains(got, "AppMag = 10") {
		t.Errorf("AppMag not halved: %q", got)
	}
}
