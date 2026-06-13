package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// parseRect splits "X,Y,W,H" into four integers. Whitespace around commas is
// allowed. W and H must be positive.
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

// resolveRectValues resolves the crop/region rectangle from either the --rect
// string or the --x/--y/--w/--h component flags on cmd. The caller passes the
// current flag values (each command binds its own globals). The two forms are
// mutually exclusive; one must be fully specified.
func resolveRectValues(cmd *cobra.Command, rectVal string, xVal, yVal, wVal, hVal int) (x, y, w, h int, err error) {
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
		return parseRect(rectVal)
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
	if wVal <= 0 || hVal <= 0 {
		return 0, 0, 0, 0, fmt.Errorf("--w and --h must be positive (got W=%d H=%d)", wVal, hVal)
	}
	return xVal, yVal, wVal, hVal, nil
}
