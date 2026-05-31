# Memory Safety Cap (Tier 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Set a soft Go memory limit (`debug.SetMemoryLimit`) by default at 75% of physical RAM so runaway conversions degrade (GC works harder) instead of OOM-ing the machine, with `--max-memory` flag and `GOMEMLIMIT` env overrides.

**Architecture:** A self-contained `internal/memlimit` package exposes a pure `Resolve(flag, env, ram)` precedence function and an impure `Apply(flag)` wrapper that reads RAM + env and calls `debug.SetMemoryLimit`. `main.go`'s existing root `PersistentPreRunE` calls `Apply` once for every command and stashes the result; `doctor` and `--verbose` report it.

**Tech Stack:** Go 1.26, cobra, `runtime/debug.SetMemoryLimit`, `golang.org/x/sys/unix` (already a transitive dep) for RAM probes, build-tagged per-OS files.

**Spec:** `docs/superpowers/specs/2026-05-30-memory-safety-cap-design.md`

**Deviations from spec (intentional simplifications):**
- No `Describe` function. `doctor` reads the `Result` already computed by `Apply` in `PersistentPreRunE` (a package var), guaranteeing it reports exactly what was applied.
- Env path reports the **raw** `GOMEMLIMIT` string (Go's size grammar differs from `--max-memory`'s), rather than re-parsing it.

---

## File Structure

- Create `internal/memlimit/memlimit.go` — `Result`, source constants, `Unlimited`, `parseMaxMemory`, `Resolve` (pure), `Apply` (impure).
- Create `internal/memlimit/ram_darwin.go` — `PhysicalRAM` via `hw.memsize`.
- Create `internal/memlimit/ram_linux.go` — `PhysicalRAM` via `Sysinfo`.
- Create `internal/memlimit/ram_other.go` — `PhysicalRAM` returns `ErrRAMUnknown`.
- Create `internal/memlimit/memlimit_test.go` — `Resolve`/parser table tests + RAM smoke test.
- Modify `cmd/wsitools/main.go` — `--max-memory` flag, `memLimitResult` var, `Apply` call + debug log + verbose line in `PersistentPreRunE`.
- Modify `cmd/wsitools/doctor.go` — `Memory:` section reading `memLimitResult`.
- Create `cmd/wsitools/doctor_test.go` — asserts `Memory:` section present.
- Create `cmd/wsitools/prerun_guard_test.go` — asserts no subcommand shadows `PersistentPreRunE`.

---

## Task 1: `memlimit` core — Result, parser, Resolve (pure)

**Files:**
- Create: `internal/memlimit/memlimit.go`
- Test: `internal/memlimit/memlimit_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/memlimit/memlimit_test.go`:

```go
package memlimit

import (
	"math"
	"testing"
)

const gib = 1 << 30
const mib = 1 << 20

func TestResolveFlag(t *testing.T) {
	tests := []struct {
		name      string
		flag      string
		wantLimit int64
		wantErr   bool
	}{
		{"bare-mib", "8000", 8000 * mib, false},
		{"gib-suffix", "12GiB", 12 * gib, false},
		{"gb-suffix", "2GB", 2_000_000_000, false},
		{"mb-suffix", "500mb", 500_000_000, false},
		{"off", "off", math.MaxInt64, false},
		{"none", "none", math.MaxInt64, false},
		{"zero", "0", math.MaxInt64, false},
		{"bogus", "banana", 0, true},
		{"zero-sized", "0mib", 0, true},
		{"negative", "-5", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(tt.flag, "", 16*gib)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Resolve(%q) expected error, got %+v", tt.flag, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve(%q) unexpected error: %v", tt.flag, err)
			}
			if got.LimitBytes != tt.wantLimit {
				t.Errorf("LimitBytes = %d, want %d", got.LimitBytes, tt.wantLimit)
			}
			if got.Source != SourceFlag {
				t.Errorf("Source = %q, want %q", got.Source, SourceFlag)
			}
			if !got.Applied {
				t.Errorf("Applied = false, want true")
			}
		})
	}
}

func TestResolveEnv(t *testing.T) {
	got, err := Resolve("", "5GiB", 16*gib)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Source != SourceEnv {
		t.Errorf("Source = %q, want %q", got.Source, SourceEnv)
	}
	if got.Applied {
		t.Errorf("Applied = true, want false (env path must not set the limit)")
	}
	if got.RawEnv != "5GiB" {
		t.Errorf("RawEnv = %q, want %q", got.RawEnv, "5GiB")
	}
}

func TestResolveDefault(t *testing.T) {
	got, err := Resolve("", "", 16*gib)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.LimitBytes != 12*gib {
		t.Errorf("LimitBytes = %d, want %d (75%% of 16GiB)", got.LimitBytes, 12*gib)
	}
	if got.Source != SourceDefault || !got.Applied {
		t.Errorf("got Source=%q Applied=%v, want default/true", got.Source, got.Applied)
	}
}

func TestResolveUnset(t *testing.T) {
	got, err := Resolve("", "", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Source != SourceUnset {
		t.Errorf("Source = %q, want %q", got.Source, SourceUnset)
	}
	if got.Applied {
		t.Errorf("Applied = true, want false (nothing to set when RAM unknown)")
	}
	if got.LimitBytes != math.MaxInt64 {
		t.Errorf("LimitBytes = %d, want Unlimited", got.LimitBytes)
	}
}

// Flag precedence beats env.
func TestResolveFlagBeatsEnv(t *testing.T) {
	got, _ := Resolve("4GiB", "5GiB", 16*gib)
	if got.Source != SourceFlag || got.LimitBytes != 4*gib {
		t.Errorf("flag must win: got Source=%q Limit=%d", got.Source, got.LimitBytes)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/memlimit/ -run TestResolve -v`
Expected: FAIL — `undefined: Resolve` (package doesn't compile yet).

- [ ] **Step 3: Write minimal implementation**

Create `internal/memlimit/memlimit.go`:

```go
// Package memlimit sets a soft Go memory limit (runtime/debug.SetMemoryLimit)
// so memory-heavy commands degrade under GC pressure instead of OOM-ing the
// host. Default = 75% of physical RAM; overridable via the --max-memory flag
// or the GOMEMLIMIT environment variable.
package memlimit

import (
	"fmt"
	"math"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

// Unlimited is the sentinel for "no soft limit" (Go's effective default).
const Unlimited int64 = math.MaxInt64

// Source labels (human-readable, used in reporting).
const (
	SourceFlag    = "--max-memory flag"
	SourceEnv     = "GOMEMLIMIT env"
	SourceDefault = "default (75% of RAM)"
	SourceUnset   = "unset (RAM undetectable)"
)

// Result describes the memory-limit decision for reporting.
type Result struct {
	LimitBytes int64  // value set (or that would be set); Unlimited == no cap
	Source     string // one of the Source* constants
	RAMBytes   uint64 // 0 when undetectable
	Applied    bool   // false when we deliberately left the runtime's value alone
	RawEnv     string // raw GOMEMLIMIT value when SourceEnv; else ""
}

// Resolve implements the precedence flag > GOMEMLIMIT env > 75% default.
// It is pure: all inputs are injected, no syscalls, no runtime mutation.
//   flagVal  — raw --max-memory value ("" if unset)
//   envVal   — value of GOMEMLIMIT ("" if unset)
//   ramBytes — physical RAM in bytes, 0 if undetectable
func Resolve(flagVal, envVal string, ramBytes uint64) (Result, error) {
	if strings.TrimSpace(flagVal) != "" {
		n, err := parseMaxMemory(flagVal)
		if err != nil {
			return Result{}, err
		}
		return Result{LimitBytes: n, Source: SourceFlag, RAMBytes: ramBytes, Applied: true}, nil
	}
	if strings.TrimSpace(envVal) != "" {
		// The runtime already consumed GOMEMLIMIT; do not re-set it. Report
		// the raw string (Go's size grammar differs from --max-memory's).
		return Result{LimitBytes: Unlimited, Source: SourceEnv, RAMBytes: ramBytes,
			Applied: false, RawEnv: envVal}, nil
	}
	if ramBytes > 0 {
		// 75% of RAM; ram/4*3 avoids intermediate overflow.
		limit := int64(ramBytes/4) * 3
		return Result{LimitBytes: limit, Source: SourceDefault, RAMBytes: ramBytes, Applied: true}, nil
	}
	return Result{LimitBytes: Unlimited, Source: SourceUnset, RAMBytes: 0, Applied: false}, nil
}

// parseMaxMemory parses a --max-memory value. Bare number = MiB. Suffixes
// MB/GB (decimal) and MiB/GiB (binary), case-insensitive. off/none/0 =
// Unlimited. Sized values must be > 0.
func parseMaxMemory(s string) (int64, error) {
	t := strings.ToLower(strings.TrimSpace(s))
	switch t {
	case "off", "none", "0":
		return Unlimited, nil
	}
	mult := float64(1 << 20) // bare number defaults to MiB
	switch {
	case strings.HasSuffix(t, "gib"):
		mult, t = 1<<30, strings.TrimSuffix(t, "gib")
	case strings.HasSuffix(t, "mib"):
		mult, t = 1<<20, strings.TrimSuffix(t, "mib")
	case strings.HasSuffix(t, "gb"):
		mult, t = 1e9, strings.TrimSuffix(t, "gb")
	case strings.HasSuffix(t, "mb"):
		mult, t = 1e6, strings.TrimSuffix(t, "mb")
	}
	val, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
	if err != nil {
		return 0, fmt.Errorf("invalid --max-memory %q: not a number (try 8000, 12GiB, or off)", s)
	}
	if val <= 0 {
		return 0, fmt.Errorf("invalid --max-memory %q: must be > 0 (use 'off' for unlimited)", s)
	}
	return int64(val * mult), nil
}

// Apply resolves the limit from the flag, the GOMEMLIMIT env, and physical
// RAM, then sets it via debug.SetMemoryLimit when the env path did not
// already own it. Returns the Result for reporting.
func Apply(flagVal string) (Result, error) {
	ram, _ := PhysicalRAM() // 0 on error → Resolve falls through to unset
	res, err := Resolve(flagVal, os.Getenv("GOMEMLIMIT"), ram)
	if err != nil {
		return Result{}, err
	}
	if res.Applied {
		debug.SetMemoryLimit(res.LimitBytes)
	}
	return res, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/memlimit/ -run TestResolve -v`
Expected: PASS (all subtests). `PhysicalRAM` is referenced by `Apply` but not yet defined — this step will fail to COMPILE until Task 2 adds it. To keep Task 1 self-contained, add a temporary stub at the end of `memlimit.go` and delete it in Task 2:

```go
// TEMP stub — replaced by build-tagged PhysicalRAM in Task 2.
func PhysicalRAM() (uint64, error) { return 0, nil }
```

Re-run: `go test ./internal/memlimit/ -run TestResolve -v` → Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/memlimit/memlimit.go internal/memlimit/memlimit_test.go
git commit -m "feat(memlimit): pure Resolve + --max-memory parser

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: `PhysicalRAM` — build-tagged RAM probes

**Files:**
- Create: `internal/memlimit/ram_darwin.go`
- Create: `internal/memlimit/ram_linux.go`
- Create: `internal/memlimit/ram_other.go`
- Modify: `internal/memlimit/memlimit.go` (remove the temp stub)
- Test: `internal/memlimit/memlimit_test.go` (add smoke test)

- [ ] **Step 1: Write the failing test**

Append to `internal/memlimit/memlimit_test.go`:

```go
func TestPhysicalRAMSmoke(t *testing.T) {
	ram, err := PhysicalRAM()
	if err != nil {
		t.Skipf("PhysicalRAM unsupported on this platform: %v", err)
	}
	if ram < 1<<30 { // sanity: any dev/CI host has >= 1 GiB
		t.Errorf("PhysicalRAM = %d bytes, implausibly small", ram)
	}
}
```

- [ ] **Step 2: Remove the temp stub and run to verify it fails**

Delete the `// TEMP stub` `PhysicalRAM` from `memlimit.go`.

Run: `go test ./internal/memlimit/ -run TestPhysicalRAMSmoke -v`
Expected: FAIL — `undefined: PhysicalRAM`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/memlimit/ram_darwin.go`:

```go
//go:build darwin

package memlimit

import "golang.org/x/sys/unix"

// PhysicalRAM returns total physical memory in bytes via the
// hw.memsize sysctl.
func PhysicalRAM() (uint64, error) {
	return unix.SysctlUint64("hw.memsize")
}
```

Create `internal/memlimit/ram_linux.go`:

```go
//go:build linux

package memlimit

import "golang.org/x/sys/unix"

// PhysicalRAM returns total physical memory in bytes via sysinfo(2).
func PhysicalRAM() (uint64, error) {
	var si unix.Sysinfo_t
	if err := unix.Sysinfo(&si); err != nil {
		return 0, err
	}
	return uint64(si.Totalram) * uint64(si.Unit), nil
}
```

Create `internal/memlimit/ram_other.go`:

```go
//go:build !darwin && !linux

package memlimit

import "errors"

// ErrRAMUnknown is returned by PhysicalRAM on platforms without a probe.
var ErrRAMUnknown = errors.New("memlimit: physical RAM detection unsupported on this platform")

// PhysicalRAM is unsupported here; callers fall back to no soft limit.
func PhysicalRAM() (uint64, error) {
	return 0, ErrRAMUnknown
}
```

- [ ] **Step 4: Run tests + go mod tidy**

Run: `go mod tidy`
Expected: `golang.org/x/sys` loses its `// indirect` comment in `go.mod` (now a direct import).

Run: `go test ./internal/memlimit/ -v`
Expected: PASS (all `Resolve` tests + `TestPhysicalRAMSmoke`).

- [ ] **Step 5: Commit**

```bash
git add internal/memlimit/ go.mod go.sum
git commit -m "feat(memlimit): build-tagged PhysicalRAM (darwin/linux)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: Wire into `main.go` — flag, Apply, debug log, verbose line

**Files:**
- Modify: `cmd/wsitools/main.go`

- [ ] **Step 1: Add the package vars and flag**

In `cmd/wsitools/main.go`, add to the `import` block:

```go
	"github.com/wsilabs/wsitools/internal/memlimit"
```

Add to the `var (...)` block (after `flagCPUProfile string`):

```go
	flagMaxMemory string

	memLimitResult memlimit.Result
```

In `func init()`, add after the `--cpu-profile` flag registration:

```go
	rootCmd.PersistentFlags().StringVar(&flagMaxMemory, "max-memory", "",
		"soft memory cap, e.g. 8000 (MiB), 12GiB, or off; default 75% of RAM")
```

- [ ] **Step 2: Call Apply in PersistentPreRunE**

In `rootCmd.PersistentPreRunE`, insert immediately after the `setupLogger()` block (before the `flagCPUProfile` handling):

```go
		res, err := memlimit.Apply(flagMaxMemory)
		if err != nil {
			return err
		}
		memLimitResult = res
		slog.Debug("memory soft limit",
			"limit", memLimitDisplay(res),
			"source", res.Source,
			"ram", formatBytes(int64(res.RAMBytes)),
			"applied", res.Applied)
		if flagVerbose && res.Source != memlimit.SourceUnset {
			fmt.Fprintf(os.Stderr, "memory soft limit: %s (%s)\n", memLimitDisplay(res), res.Source)
		}
```

- [ ] **Step 3: Add the shared display helper**

At the end of `cmd/wsitools/main.go`, add:

```go
// memLimitDisplay renders a Result's limit for human output: the raw
// GOMEMLIMIT string on the env path, "none (unlimited)" for an uncapped
// limit, otherwise a human byte size.
func memLimitDisplay(r memlimit.Result) string {
	if r.Source == memlimit.SourceEnv {
		return r.RawEnv
	}
	if r.LimitBytes == memlimit.Unlimited {
		return "none (unlimited)"
	}
	return formatBytes(r.LimitBytes)
}
```

- [ ] **Step 4: Build and exercise it manually**

Run: `make build`
Expected: builds clean (ignore the `duplicate libraries` linker warning).

Run: `GOMEMLIMIT= ./bin/wsitools --log-level debug version 2>&1 | grep "memory soft limit"`
Expected: a debug line, e.g. `... msg="memory soft limit" limit=12.0GB source="default (75% of RAM)" ... applied=true`.

Run: `./bin/wsitools --max-memory off --log-level debug version 2>&1 | grep "memory soft limit"`
Expected: `limit="none (unlimited)" source="--max-memory flag" ... applied=true`.

Run: `./bin/wsitools --max-memory bogus version`
Expected: exits non-zero with `error: invalid --max-memory "bogus": not a number (try 8000, 12GiB, or off)`.

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/main.go
git commit -m "feat(cli): default soft memory cap + --max-memory flag

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: `doctor` Memory section

**Files:**
- Modify: `cmd/wsitools/doctor.go`
- Test: `cmd/wsitools/doctor_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/wsitools/doctor_test.go`:

```go
package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestDoctorReportsMemory runs the built binary's `doctor` and checks the
// Memory section is present with a Soft limit line.
func TestDoctorReportsMemory(t *testing.T) {
	bin := stripedBinary(t) // reuse helper from striped_formats_test.go
	out, err := exec.Command(bin, "doctor").CombinedOutput()
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out)
	}
	text := string(out)
	if !strings.Contains(text, "Memory:") {
		t.Errorf("doctor output missing 'Memory:' section:\n%s", text)
	}
	if !strings.Contains(text, "Soft limit:") {
		t.Errorf("doctor output missing 'Soft limit:' line:\n%s", text)
	}
	_ = filepath.Separator // keep filepath imported if helper changes
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `make build && go test ./cmd/wsitools/ -run TestDoctorReportsMemory -v`
Expected: FAIL — output has no `Memory:` section.

- [ ] **Step 3: Add the Memory section**

In `cmd/wsitools/doctor.go`, replace the final `fmt.Println("Reader: ...")` + `return nil` with:

```go
		fmt.Println("Reader: opentile-go (see go.mod for version)")
		fmt.Println()
		fmt.Println("Memory:")
		if memLimitResult.RAMBytes > 0 {
			fmt.Printf("  Physical RAM:  %s\n", formatBytes(int64(memLimitResult.RAMBytes)))
		} else {
			fmt.Println("  Physical RAM:  unknown")
		}
		fmt.Printf("  Soft limit:    %s  (source: %s)\n",
			memLimitDisplay(memLimitResult), memLimitResult.Source)
		return nil
```

(`memLimitResult` and `memLimitDisplay` are populated by `PersistentPreRunE`, which cobra runs before this `RunE`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `make build && go test ./cmd/wsitools/ -run TestDoctorReportsMemory -v`
Expected: PASS.

Also eyeball it: `./bin/wsitools doctor | tail -5`
Expected:
```
Memory:
  Physical RAM:  16.0 GB
  Soft limit:    12.0 GB  (source: default (75% of RAM))
```

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/doctor.go cmd/wsitools/doctor_test.go
git commit -m "feat(doctor): report physical RAM + active soft memory limit

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Guard test — no subcommand shadows PersistentPreRunE

**Files:**
- Create: `cmd/wsitools/prerun_guard_test.go`

The global cap relies on the root `PersistentPreRunE`. Cobra runs only the
*most specific* one, so a subcommand defining its own would silently skip
the cap. This test fails loudly if that ever happens.

- [ ] **Step 1: Write the test**

Create `cmd/wsitools/prerun_guard_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test ./cmd/wsitools/ -run TestNoSubcommandShadowsPersistentPreRun -v`
Expected: PASS (no subcommand currently defines one).

- [ ] **Step 3: Commit**

```bash
git add cmd/wsitools/prerun_guard_test.go
git commit -m "test(cli): guard against subcommand shadowing PersistentPreRunE

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: End-to-end verification

**Files:** none (verification only)

- [ ] **Step 1: Full unit + race suite**

Run: `make test`
Expected: all packages `ok`, including `internal/memlimit` and `cmd/wsitools`. (Integration tests that need fixtures self-skip when `WSI_TOOLS_TESTDIR` is unset.)

- [ ] **Step 2: Verify the cap actually binds and the job still completes**

This proves the safety property: with a tight cap, peak RSS stays near the
limit and the conversion still finishes (degrades, not crashes).

```bash
OUT=$(mktemp -d)
/usr/bin/time -l ./bin/wsitools --max-memory 1500 convert --to dzi \
  -o "$OUT/o.dzi" sample_files/ndpi/CMU-1.ndpi >/dev/null 2>/tmp/cap.txt
grep -E ' real| maximum resident' /tmp/cap.txt
rm -rf "$OUT"
```

Expected: completes exit 0; `maximum resident set size` well below the
unlimited-run baseline (~3.5 GB) — on the order of ~1.6–2.0 GB (the soft
limit plus non-heap/overhead). Wall time somewhat higher than default.

- [ ] **Step 3: Verify default vs off**

```bash
./bin/wsitools doctor | grep -A2 "Memory:"
GOMEMLIMIT=4GiB ./bin/wsitools doctor | grep "Soft limit:"
./bin/wsitools --max-memory off doctor | grep "Soft limit:"
```

Expected, respectively: default `(source: default (75% of RAM))`;
`(source: GOMEMLIMIT env)` showing `4GiB`; `none (unlimited) (source: --max-memory flag)`.

- [ ] **Step 4: Vet**

Run: `make vet`
Expected: no findings.

- [ ] **Step 5: Final commit (if any docs/cleanup) — otherwise done**

The feature branch `feat/memory-safety-cap` now holds the spec, the bench
enhancement, and Tasks 1–5. Hand back for review / merge.

---

## Self-Review notes

- **Spec coverage:** D1 global hook (Task 3 PersistentPreRunE) ✓; D2 75% formula (Task 1 Resolve + test) ✓; D3 doctor+debug+verbose (Tasks 3,4) ✓; D4 precedence flag>env>default (Task 1 tests incl. FlagBeatsEnv) ✓; D5 internal/memlimit pure+impure split (Tasks 1,2) ✓. Edge cases: RAM-undetectable (TestResolveUnset, ram_other.go) ✓; subcommand shadowing (Task 5) ✓; invalid flag fail-fast (Task 3 Step 4 manual + Task 1 parser test) ✓.
- **Type consistency:** `Result` fields (`LimitBytes`, `Source`, `RAMBytes`, `Applied`, `RawEnv`) used identically across Tasks 1/3/4. `memLimitDisplay`/`memLimitResult` defined in Task 3, consumed in Task 4. `formatBytes` is the existing helper in `downsample.go`.
- **Deviation logged:** `Describe` dropped; doctor reads `memLimitResult`. Env value reported raw.
```
