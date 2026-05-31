package main

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestNoSubcommandShadowsPersistentPreRun ensures only rootCmd defines a
// PersistentPreRun(E). Cobra invokes only the most specific one, so a
// subcommand defining its own would bypass the global memory-limit setup.
func TestNoSubcommandShadowsPersistentPreRun(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.PersistentPreRunE != nil || sub.PersistentPreRun != nil {
				t.Errorf("subcommand %q defines its own PersistentPreRun(E); "+
					"it must not, or memlimit.Apply will be skipped. Fold its "+
					"logic into rootCmd's hook instead.", sub.Name())
			}
			walk(sub)
		}
	}
	walk(rootCmd)
}
