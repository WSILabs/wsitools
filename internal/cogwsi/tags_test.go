package cogwsi

import "testing"

func TestNewTagIDsDoNotCollide(t *testing.T) {
	pairs := []struct {
		name string
		id   uint16
	}{
		{"WSIImageType", TagWSIImageType},
		{"WSILevelIndex", TagWSILevelIndex},
		{"WSILevelCount", TagWSILevelCount},
		{"WSISourceFormat", TagWSISourceFormat},
		{"WSIToolsVersion", TagWSIToolsVersion},
		{"WSIMPPX", TagWSIMPPX},
		{"WSIMPPY", TagWSIMPPY},
		{"WSIMagnification", TagWSIMagnification},
	}
	seen := map[uint16]string{}
	for _, p := range pairs {
		if prev, dup := seen[p.id]; dup {
			t.Errorf("tag id %d used by both %s and %s", p.id, prev, p.name)
		}
		seen[p.id] = p.name
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
