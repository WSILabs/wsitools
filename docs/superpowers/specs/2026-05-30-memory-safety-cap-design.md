# wsitools — default memory safety cap (Tier 1)

**Status:** IMPLEMENTED (2026-05-31, branch `feat/memory-safety-cap`).
**Scope:** Tier 1 of the memory-footprint work. Tier 2 (cascade
streaming redesign) is out of scope.

**Implementation deviations** (see the plan
`docs/superpowers/plans/2026-05-30-memory-safety-cap.md` for rationale):
- **No `Describe()` function.** `doctor` reads the `Result` already
  computed by `Apply` in the root `PersistentPreRunE` (the package var
  `memLimitResult`), which cobra always runs before any subcommand's
  `RunE`. This is simpler (YAGNI) and guarantees `doctor` reports exactly
  what was applied. The §3.1/§4.2 `Describe` references below are
  superseded.
- **Env path reports the raw `GOMEMLIMIT` string** rather than
  re-parsing it (Go's size grammar differs from `--max-memory`'s), so
  `Result.LimitBytes` is `Unlimited` on the env path and `RawEnv` carries
  the display value.

---

## 1. Problem

`wsitools convert --to dzi` (and other pixel pipelines) can balloon the
Go heap on wide whole-slide images. On a 16 GB Mac, benchmarking drove
the machine into the macOS "Your system has run out of application
memory" panic. Investigation (see `project_v0.21_dzi_memory` notes and
the opentile-go v0.30 milestone) showed:

- Most of the footprint is **Go GC headroom**, not live data. Probing
  `GOMEMLIMIT` recovered 40–60% of peak RSS for a few percent of time
  (CMU-1.ndpi: 3.5 GB → 1.45 GB at +4% time; OS-2.ndpi: 5.4 GB → 3.2 GB
  at +5% time with a 2 GiB limit).
- The remaining "floor" is wsitools' own DZI cascade (full-width
  `levelBuilder` buffers) — a Tier 2 concern.

The single cheapest, highest-value mitigation is a **soft memory limit**
set by default so a runaway conversion degrades (GC works harder →
slower) instead of OOM-ing the machine. This spec covers only that.

### Non-goals

- No conversion-pipeline / cascade redesign (Tier 2).
- No reliance on the operator exporting anything — the default is
  automatic and internal.
- The cap is a **soft** limit: it does not change behaviour on slides
  whose footprint stays below it (the common case). It only engages
  under memory pressure.

---

## 2. Decisions (sealed)

| # | Decision | Choice |
|---|---|---|
| D1 | Scope | **Global** — set once in the existing root `PersistentPreRunE`; no-op for metadata commands, automatically covers `convert`/`downsample`/`hash --mode pixel`/`region` and any future command. |
| D2 | Formula | **75% of total physical RAM** (`limit = RAM × 3/4`). Predictable, leaves 25% for OS + other apps; clears all measured peaks (only bites > ~12 GB on a 16 GB box). |
| D3 | Visibility | Report in **`doctor`** (`Memory:` section), at **debug log** level on startup, and in the **`--verbose`** run summary. Silent on normal runs. |
| D4 | Override | Precedence **`--max-memory` flag > `GOMEMLIMIT` env > 75% default**. Env respected when present (incl. `off`); flag added now (Tier 1 includes it). |
| D5 | Implementation | Self-contained **`internal/memlimit`** package; pure `Resolve` + impure `Apply`; build-tagged RAM probes. |

---

## 3. Package `internal/memlimit`

```
internal/memlimit/
  memlimit.go      // Result, Resolve (pure), Apply (impure), errors
  ram_darwin.go    //go:build darwin  → unix.SysctlUint64("hw.memsize")
  ram_linux.go     //go:build linux   → unix.Sysinfo{} → Totalram * Unit
  ram_other.go     //go:build !darwin && !linux → ErrRAMUnknown
  memlimit_test.go
```

`golang.org/x/sys` is already a (transitive) dependency; this promotes
`golang.org/x/sys/unix` to a direct import.

### 3.1 API

```go
// Unlimited is the sentinel for "no soft limit" (Go's effective default).
const Unlimited int64 = math.MaxInt64

type Result struct {
    LimitBytes int64  // value set (or that would be set); Unlimited == no cap
    Source     string // SourceFlag | SourceEnv | SourceDefault | SourceUnset
    RAMBytes   uint64 // 0 when undetectable
    Applied    bool   // false when we deliberately left the runtime's value (env path)
    RawEnv     string // raw GOMEMLIMIT value when SourceEnv and unparseable; else ""
}

// Pure: all inputs injected, no syscalls, no runtime mutation.
//   flagVal — raw --max-memory value ("" if unset)
//   envVal  — os.Getenv("GOMEMLIMIT") ("" if unset)
//   ramBytes — physical RAM, 0 if undetectable
func Resolve(flagVal, envVal string, ramBytes uint64) (Result, error)

// Impure: reads GOMEMLIMIT + PhysicalRAM, calls debug.SetMemoryLimit
// when Resolve says to. Returns the Result for reporting.
func Apply(flagVal string) (Result, error)

// Resolve-only helper for read-only reporting (doctor/version) that must
// not mutate the runtime.
func Describe(flagVal string) (Result, error)

func PhysicalRAM() (uint64, error)
```

### 3.2 `Resolve` precedence

1. **`flagVal != ""`** → parse (see §3.3).
   - `off`/`none`/`0` → `{Unlimited, SourceFlag, ram, Applied:true}`.
   - valid size → `{size, SourceFlag, ram, true}`.
   - invalid → error.
2. **`envVal != ""`** → `{LimitBytes: <see below>, SourceEnv, ram, Applied:false}`.
   We do **not** re-set the limit; the runtime already consumed
   `GOMEMLIMIT`. `LimitBytes` is a *display-only* best-effort parse of
   `envVal` using Go's size syntax (`off` → `Unlimited`); if it can't be
   parsed, set `LimitBytes = Unlimited` and carry the raw string in a
   `Result.RawEnv` field for reporting. `Applied:false` (nothing set).
3. **`ramBytes > 0`** → `{ramBytes*3/4, SourceDefault, ram, true}`.
4. **else** → `{Unlimited, SourceUnset, 0, false}`.

`Apply` calls `debug.SetMemoryLimit(r.LimitBytes)` **iff** `r.Applied`.

### 3.3 `--max-memory` value format

- Bare number → **MiB** (e.g. `8000` = 8000 MiB). Friendlier than Go's
  bytes-default.
- Unit suffixes: `MB`, `GB`, `MiB`, `GiB` (case-insensitive).
- `off` / `none` / `0` → unlimited.
- Anything else → error `invalid --max-memory %q: <reason>`.

(Decimal `MB`/`GB` = ×10⁶/10⁹; binary `MiB`/`GiB` = ×2²⁰/2³⁰.)

---

## 4. Integration

### 4.1 `cmd/wsitools/main.go`

- New persistent flag:
  `rootCmd.PersistentFlags().StringVar(&flagMaxMemory, "max-memory", "", "soft memory cap (e.g. 8000, 12GiB, off); default 75% of RAM")`.
- In `PersistentPreRunE`, after `setupLogger()`:
  ```go
  res, err := memlimit.Apply(flagMaxMemory)
  if err != nil {
      return err // invalid --max-memory → fail fast
  }
  memLimitResult = res // package var, for --verbose + reporting
  slog.Debug("memory soft limit",
      "limit", humanizeBytes(res.LimitBytes), "source", res.Source,
      "ram", humanizeBytes(int64(res.RAMBytes)), "applied", res.Applied)
  ```

### 4.2 `doctor`

Append:
```
Memory:
  Physical RAM:  16.0 GiB
  Soft limit:    12.0 GiB  (75% of RAM; source: default)
```
- Undetectable RAM → `Physical RAM:  unknown` and `Soft limit: none`.
- `Unlimited` limit → `Soft limit:    none (unlimited)`.
- Uses `memlimit.Describe` (read-only; must not call `SetMemoryLimit`).

### 4.3 `--verbose`

One line at run start (where other per-run verbose detail prints):
`memory soft limit: 12.0 GiB (default, 75% of 16.0 GiB RAM)`.
Suppressed when source is `unset`.

---

## 5. Edge cases & invariants

- **RAM undetectable, no flag/env:** no limit set; debug-log it; proceed
  (today's behaviour). Never fatal.
- **Subcommand shadowing:** cobra runs only the most specific
  `PersistentPreRunE`. Guard test (§6) asserts no subcommand defines
  its own, so the global hook always fires.
- **`Describe` purity:** `doctor`/`version` must never mutate the
  runtime limit — they call `Describe`, not `Apply`.
- **Idempotence:** `Apply` runs once per process (single
  `PersistentPreRunE`); no re-entrancy concern.
- **Very low `--max-memory`:** honoured as requested; GC may thrash —
  the user's explicit choice; noted in flag help.
- **GOGC interaction:** none required; the soft limit coexists with
  `GOGC`. We never disable GC.

---

## 6. Testing

- **`Resolve` table test** (pure): flag set (size / `off` / `0` /
  invalid), env set, neither, RAM = 0; assert `LimitBytes`, `Source`,
  `Applied`, and 75% rounding. No syscalls.
- **`--max-memory` parser test:** bare MiB, each suffix, `off`/`none`,
  invalid strings.
- **`PhysicalRAM` smoke test:** returns > 0 on the host (darwin/linux).
- **Guard test:** walk `rootCmd.Commands()` (recursively) and assert
  none set `PersistentPreRunE`/`PersistentPreRun`.
- **`doctor` test:** output contains a `Memory:` section with a
  `Soft limit:` line.

---

## 7. Success criteria

- Default run on any slide whose footprint is < 75% RAM is byte-identical
  in output and within noise on time vs. today (no regression).
- A conversion that would exceed ~75% RAM stays under the cap (GC
  engages) instead of OOM-ing the machine — verified by forcing a low
  `--max-memory` on a large fixture and observing peak RSS ≤ cap with
  the job still completing.
- `--max-memory off` and `GOMEMLIMIT=off` both yield the pre-Tier-1
  (unlimited) behaviour.
- `doctor` reports the active cap and its source.

---

## 8. References

- opentile-go v0.30 memory-budget milestone (read-path byte budgets) —
  complementary, library-side.
- Bench data: `scripts/bench-dzi.sh` (now reports peak RSS) + the
  `GOMEMLIMIT` probes in this session.
- `runtime/debug.SetMemoryLimit` (Go 1.19+; project on 1.26).
