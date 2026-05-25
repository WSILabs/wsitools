package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// Package-level flag globals (cobra binds Var/IntVar/StringVar into
// these). Reset at test entry where mutation matters.
var (
	regionLevel  int
	regionRect   string
	regionX      int
	regionY      int
	regionW      int
	regionH      int
	regionImage  int
	regionFormat string
	regionOutput string
	regionForce  bool
)

// parseRect splits "X,Y,W,H" into four integers. Whitespace around
// commas is allowed. Returns an error if the format or value
// constraints don't match.
func parseRect(s string) (x, y, w, h int, err error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return 0, 0, 0, 0, fmt.Errorf("--rect: expected X,Y,W,H (4 comma-separated integers), got %q", s)
	}
	vals := make([]int, 4)
	for i, p := range parts {
		v, e := strconv.Atoi(strings.TrimSpace(p))
		if e != nil {
			return 0, 0, 0, 0, fmt.Errorf("--rect: not an integer: %q", strings.TrimSpace(p))
		}
		vals[i] = v
	}
	if vals[2] <= 0 || vals[3] <= 0 {
		return 0, 0, 0, 0, fmt.Errorf("--rect: W and H must be positive (got W=%d H=%d)", vals[2], vals[3])
	}
	return vals[0], vals[1], vals[2], vals[3], nil
}

// resolveRect figures out which form the user used (--rect vs
// --x/--y/--w/--h) and returns the resolved rectangle.
func resolveRect(cmd *cobra.Command) (x, y, w, h int, err error) {
	rectSet := cmd.Flags().Changed("rect")
	xSet := cmd.Flags().Changed("x")
	ySet := cmd.Flags().Changed("y")
	wSet := cmd.Flags().Changed("w")
	hSet := cmd.Flags().Changed("h")
	anyIndividual := xSet || ySet || wSet || hSet

	if rectSet && anyIndividual {
		return 0, 0, 0, 0, fmt.Errorf("--rect and --x/--y/--w/--h are mutually exclusive; use one form or the other")
	}
	if rectSet {
		return parseRect(regionRect)
	}
	if !anyIndividual {
		return 0, 0, 0, 0, fmt.Errorf("must specify either --rect or all of --x/--y/--w/--h")
	}
	missing := []string{}
	if !xSet {
		missing = append(missing, "--x")
	}
	if !ySet {
		missing = append(missing, "--y")
	}
	if !wSet {
		missing = append(missing, "--w")
	}
	if !hSet {
		missing = append(missing, "--h")
	}
	if len(missing) > 0 {
		return 0, 0, 0, 0, fmt.Errorf("must specify all of --x/--y/--w/--h (missing: %s)", strings.Join(missing, " "))
	}
	if regionW <= 0 || regionH <= 0 {
		return 0, 0, 0, 0, fmt.Errorf("--w and --h must be positive (got W=%d H=%d)", regionW, regionH)
	}
	return regionX, regionY, regionW, regionH, nil
}
