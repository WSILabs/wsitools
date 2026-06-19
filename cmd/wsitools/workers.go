package main

// resolveWorkers reconciles a command's primary worker flag with its alias
// (--workers ⇄ --jobs). An explicitly-set primary wins; otherwise an
// explicitly-set alias applies; otherwise the primary's value (its default) is
// used.
func resolveWorkers(primary int, primarySet bool, alias int, aliasSet bool) int {
	if primarySet {
		return primary
	}
	if aliasSet {
		return alias
	}
	return primary
}
