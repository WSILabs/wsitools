package main

import "testing"

func TestResolveWorkers(t *testing.T) {
	cases := []struct {
		name       string
		primary    int
		primarySet bool
		alias      int
		aliasSet   bool
		want       int
	}{
		{"neither set uses primary default", 8, false, 0, false, 8},
		{"primary set", 4, true, 0, false, 4},
		{"alias set", 8, false, 6, true, 6},
		{"both set primary wins", 4, true, 6, true, 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveWorkers(c.primary, c.primarySet, c.alias, c.aliasSet); got != c.want {
				t.Fatalf("got %d, want %d", got, c.want)
			}
		})
	}
}
