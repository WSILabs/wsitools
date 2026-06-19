package main

import "fmt"

// resolveConvertTarget picks the output container for `convert`. An explicit --to
// wins; an empty --to is inferred from the source format (the format-preserving
// default that makes `convert in out` and `convert --rect` subsume crop), using
// the same source→container mapping as downsample/crop. An un-inferable source
// errors, asking the user to specify --to.
func resolveConvertTarget(to, srcFormat string) (string, error) {
	if to != "" {
		return to, nil
	}
	target, ok := downsampleTargetForFormat(srcFormat)
	if !ok {
		return "", fmt.Errorf("cannot infer output container for source format %q; specify --to", srcFormat)
	}
	return target, nil
}
