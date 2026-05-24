package tileorder

import (
	"fmt"
	"sort"
	"sync"
)

// OrderStrategy maps tile grid coordinates to a linear emission index.
// Implementations MUST be:
//   - bijective on the [0, tilesX) × [0, tilesY) domain
//   - deterministic across calls for the same (x, y, tilesX, tilesY)
//   - safe for concurrent calls (Submit happens from N worker goroutines)
//
// Strategies MAY perform per-(tilesX, tilesY) memoization (sync.Map keyed
// by grid shape) — required for non-power-of-2 grids with Hilbert and
// Morton orderings.
type OrderStrategy interface {
	// Name returns the canonical lowercase identifier ("row-major",
	// "hilbert", "morton"). Used for ByName lookup and error messages.
	Name() string

	// Index returns the linear emission index for tile (x, y) in a
	// tilesX × tilesY grid. Result is in [0, tilesX*tilesY).
	Index(x, y, tilesX, tilesY uint32) uint32

	// IndexToXY is the inverse of Index.
	IndexToXY(idx, tilesX, tilesY uint32) (x, y uint32)
}

var (
	regMu sync.RWMutex
	reg   = map[string]OrderStrategy{}
)

// register adds a strategy to the package-level registry. Called from
// each strategy file's init().
func register(s OrderStrategy) {
	regMu.Lock()
	defer regMu.Unlock()
	reg[s.Name()] = s
}

// ByName returns the strategy with the given canonical name, or an error
// for unknown names.
func ByName(name string) (OrderStrategy, error) {
	regMu.RLock()
	defer regMu.RUnlock()
	s, ok := reg[name]
	if !ok {
		return nil, fmt.Errorf("tileorder: unknown strategy %q (known: %v)", name, knownLocked())
	}
	return s, nil
}

// Known returns the names of all registered strategies, sorted.
func Known() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	return knownLocked()
}

func knownLocked() []string {
	out := make([]string, 0, len(reg))
	for n := range reg {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
