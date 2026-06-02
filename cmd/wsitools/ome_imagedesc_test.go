package main

import (
	"strings"
	"testing"
)

func TestSyntheticOMEDescriptionAssociated(t *testing.T) {
	assoc := []OMEAssoc{
		{Name: "label", W: 100, H: 80},
		{Name: "macro", W: 600, H: 400},
	}
	xml := SyntheticOMEDescription(2220, 2967, 0.5, 0.5, "Image", "Aperio", assoc)

	if !strings.Contains(xml, "<!-- Warning: this comment is an OME-XML metadata block") {
		t.Errorf("missing OME preamble comment:\n%s", xml)
	}
	if !strings.HasSuffix(strings.TrimSpace(xml), "OME>") {
		t.Errorf("OME-XML must end with OME> for detection:\n%s", xml)
	}
	if !strings.Contains(xml, `Name="Image"`) || !strings.Contains(xml, `IFD="0"`) {
		t.Errorf("missing main image / IFD=0:\n%s", xml)
	}
	if !strings.Contains(xml, `Name="label"`) || !strings.Contains(xml, `IFD="1"`) {
		t.Errorf("missing label at IFD=1:\n%s", xml)
	}
	if !strings.Contains(xml, `Name="macro"`) || !strings.Contains(xml, `IFD="2"`) {
		t.Errorf("missing macro at IFD=2:\n%s", xml)
	}
	if got := strings.Count(xml, "<Image "); got != 3 {
		t.Errorf("Image count = %d, want 3 (main + 2 associated)", got)
	}
	if !strings.Contains(xml, `SizeX="600" SizeY="400"`) {
		t.Errorf("macro Pixels missing its dims:\n%s", xml)
	}
}

func TestSyntheticOMEDescriptionNoAssociated(t *testing.T) {
	xml := SyntheticOMEDescription(10, 10, 0, 0, "Image", "", nil)
	if got := strings.Count(xml, "<Image "); got != 1 {
		t.Errorf("Image count = %d, want 1", got)
	}
	if !strings.HasSuffix(strings.TrimSpace(xml), "OME>") {
		t.Errorf("must end with OME>:\n%s", xml)
	}
}
