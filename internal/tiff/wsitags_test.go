package tiff

import "testing"

func TestWSIImageTypeConstants(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"Pyramid", WSIImageTypePyramid, "pyramid"},
		{"Label", WSIImageTypeLabel, "label"},
		{"Macro", WSIImageTypeMacro, "macro"},
		{"Overview", WSIImageTypeOverview, "overview"},
		{"Thumbnail", WSIImageTypeThumbnail, "thumbnail"},
		{"Probability", WSIImageTypeProbability, "probability"},
		{"Map", WSIImageTypeMap, "map"},
		{"Associated", WSIImageTypeAssociated, "associated"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %q want %q", c.name, c.got, c.want)
		}
	}
}

func TestValidateWSIImageTypeAcceptsCanonical(t *testing.T) {
	for _, v := range []string{
		WSIImageTypePyramid, WSIImageTypeLabel, WSIImageTypeMacro,
		WSIImageTypeOverview, WSIImageTypeThumbnail,
		WSIImageTypeProbability, WSIImageTypeMap, WSIImageTypeAssociated,
	} {
		if err := ValidateWSIImageType(v); err != nil {
			t.Errorf("ValidateWSIImageType(%q): unexpected error %v", v, err)
		}
	}
}

func TestValidateWSIImageTypeRejectsUnknown(t *testing.T) {
	for _, v := range []string{"", "Pyramid", "labels", "macros", "unknown"} {
		if err := ValidateWSIImageType(v); err == nil {
			t.Errorf("ValidateWSIImageType(%q): expected error, got nil", v)
		}
	}
}
