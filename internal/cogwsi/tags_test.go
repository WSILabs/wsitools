package cogwsi

import "testing"

func TestNewTagIDsDoNotCollide(t *testing.T) {
	ids := map[uint16]string{
		TagWSIImageType:     "WSIImageType",
		TagWSILevelIndex:    "WSILevelIndex",
		TagWSILevelCount:    "WSILevelCount",
		TagWSISourceFormat:  "WSISourceFormat",
		TagWSIToolsVersion:  "WSIToolsVersion",
		TagWSIMPPX:          "WSIMPPX",
		TagWSIMPPY:          "WSIMPPY",
		TagWSIMagnification: "WSIMagnification",
	}
	seen := map[uint16]string{}
	for id, name := range ids {
		if prev, dup := seen[id]; dup {
			t.Errorf("tag id %d used by both %s and %s", id, prev, name)
		}
		seen[id] = name
	}
}

func TestNewTagIDsAreInPrivateRange(t *testing.T) {
	for _, id := range []uint16{TagWSIMPPX, TagWSIMPPY, TagWSIMagnification} {
		if id < 32768 {
			t.Errorf("tag id %d outside TIFF private range (>=32768)", id)
		}
	}
}

func TestNewTagIDValues(t *testing.T) {
	cases := []struct {
		got  uint16
		want uint16
		name string
	}{
		{TagWSIMPPX, 65085, "WSIMPPX"},
		{TagWSIMPPY, 65086, "WSIMPPY"},
		{TagWSIMagnification, 65087, "WSIMagnification"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %d want %d", c.name, c.got, c.want)
		}
	}
}
