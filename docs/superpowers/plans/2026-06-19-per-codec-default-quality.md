# Per-codec default quality — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Checkbox (`- [ ]`) steps.

**Goal:** Absent `--quality` uses a per-codec default (q85 for q-codecs; jxl
distance-1.0) instead of a forced q=90. Explicit `--quality` unchanged.

**Spec:** `docs/superpowers/specs/2026-06-19-per-codec-default-quality-design.md`.

**Branch:** `feat/per-codec-default-quality` (off main@8a49511).

**Change points:** `codec_resolve.go:20` (`knobs := {"q": fallbackQ}`),
`convert_tiff.go:441` (`parseQualityKnobs` `{"q":"90"}`).

---

### Task 1: `codecDefaultKnobs` + `qFromKnobs` (pure helpers)

**Files:** `cmd/wsitools/codec_resolve.go`, `cmd/wsitools/codec_resolve_test.go`.

- [ ] **Step 1: Failing test**

Append to `codec_resolve_test.go`:

```go
func TestCodecDefaultKnobs(t *testing.T) {
	for _, c := range []string{"jpeg", "jpeg2000", "htj2k", "avif", "webp"} {
		if got := codecDefaultKnobs(c); got["q"] != "85" {
			t.Errorf("%s default: %v, want q=85", c, got)
		}
	}
	if got := codecDefaultKnobs("jpegxl"); got["distance"] != "1.0" {
		t.Errorf("jpegxl default: %v, want distance=1.0", got)
	}
	if got := codecDefaultKnobs("png"); len(got) != 0 {
		t.Errorf("png default: %v, want empty", got)
	}
	if got := codecDefaultKnobs("unknown"); got["q"] != "85" {
		t.Errorf("unknown default: %v, want q=85", got)
	}
}

func TestQFromKnobs(t *testing.T) {
	if qFromKnobs(map[string]string{"q": "70"}) != 70 {
		t.Error("q=70")
	}
	if qFromKnobs(map[string]string{"distance": "1.0"}) != 85 {
		t.Error("no q → 85")
	}
	if qFromKnobs(map[string]string{}) != 85 {
		t.Error("empty → 85")
	}
	if qFromKnobs(map[string]string{"q": "999"}) != 85 {
		t.Error("out-of-range q → 85")
	}
}
```

- [ ] **Step 2: Run — FAIL** (`codecDefaultKnobs`/`qFromKnobs` undefined)

`go test ./cmd/wsitools/ -run 'CodecDefaultKnobs|QFromKnobs'`

- [ ] **Step 3: Implement** in `codec_resolve.go`:

```go
// codecDefaultKnobs is wsitools' single source of truth for each codec's default
// encode knobs when --quality is absent. Values start from the codecs' own encoder
// defaults (q=85 for the q-scale codecs; jpegxl's native "visually lossless"
// distance 1.0). A forced uniform "q" would mis-set codecs whose quality scale
// isn't 1–100 (notably jpegxl, where q=90 maps to a MORE-lossy distance 1.5).
func codecDefaultKnobs(codec string) map[string]string {
	switch codec {
	case "jpegxl":
		return map[string]string{"distance": "1.0"}
	case "png":
		return map[string]string{}
	default: // jpeg, jpeg2000, htj2k, avif, webp, and unknown
		return map[string]string{"q": "85"}
	}
}

// qFromKnobs extracts a 1–100 integer quality from knobs for metadata that needs a
// number (the Aperio "Q=" token). Returns 85 when "q" is absent or invalid.
func qFromKnobs(knobs map[string]string) int {
	if v, ok := knobs["q"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 100 {
			return n
		}
	}
	return 85
}
```

- [ ] **Step 4: Run — PASS**; `gofmt -l` clean.

- [ ] **Step 5: Commit**

```bash
git add cmd/wsitools/codec_resolve.go cmd/wsitools/codec_resolve_test.go
git commit -m "$(cat <<'EOF'
feat(codec): per-codec default-knobs map + qFromKnobs

codecDefaultKnobs is the single source of truth for each codec's default
encode knobs (q=85 q-codecs; jpegxl distance=1.0; png none). qFromKnobs
derives an int quality (default 85) for metadata. Wired next.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Wire the per-codec default through resolve + callers

**Files:** `cmd/wsitools/codec_resolve.go`, `cmd/wsitools/convert_tiff.go`
(`parseQualityKnobs`), `cmd/wsitools/convert_factor.go` (`runConvertFactor` int),
tests.

- [ ] **Step 1: `resolveTransformCodec` uses the per-codec default**

In `resolveTransformCodec` (`codec_resolve.go`), replace the seed:
```go
	knobs := map[string]string{"q": strconv.Itoa(fallbackQ)}
	if quality != "" {
		parsed, err := parseQualityKnobs(quality)
		if err != nil {
			return nil, nil, "", err
		}
		knobs = parsed
	}
	if codecName == "" || codecName == "jpeg" {
		return jpegcodec.Factory{}, knobs, "jpeg", nil
	}
	...
```
with: resolve the codec NAME first, default the knobs from `codecDefaultKnobs(name)`,
override only when `--quality` is given:
```go
	name := codecName
	if name == "" {
		name = "jpeg"
	}
	knobs := codecDefaultKnobs(name)
	if quality != "" {
		parsed, err := parseQualityKnobs(quality)
		if err != nil {
			return nil, nil, "", err
		}
		knobs = parsed
	}
	if name == "jpeg" {
		return jpegcodec.Factory{}, knobs, "jpeg", nil
	}
	fac, err := codec.Lookup(name)
	if err != nil {
		return nil, nil, "", err
	}
	return fac, knobs, name, nil
```
The `fallbackQ` parameter is now unused inside the function — **drop it** from the
signature (`resolveTransformCodec(codecName, quality string)`), and update all call
sites (`grep -rn resolveTransformCodec cmd/wsitools/`) to drop the int arg.
(Callers that needed an int quality derive it via `qFromKnobs(knobs)` after the
call — Step 3.)

- [ ] **Step 2: `parseQualityKnobs` default 90 → 85**

`convert_tiff.go:441`: `{"q": "90"}` → `{"q": "85"}`. (Only affects a user
`--quality` string that omits `q`, e.g. `reversible=true`; consistent with the
codec defaults.)

- [ ] **Step 3: Callers derive int quality via `qFromKnobs`**

`runConvertFactor` (`convert_factor.go`): it currently does `knobs, _ :=
parseQualityKnobs(cvQuality); quality, _ := strconv.Atoi(knobs["q"])` to get the int.
That's the **user** path. But the per-codec default must apply when `cvQuality == ""`.
Change it to resolve the codec default for the int too:
```go
	var knobs map[string]string
	if cvQuality == "" {
		cn := cvCodec
		if cn == "" {
			cn = "jpeg"
		}
		knobs = codecDefaultKnobs(cn)
	} else {
		var qerr error
		knobs, qerr = parseQualityKnobs(cvQuality)
		if qerr != nil {
			return qerr
		}
	}
	quality := qFromKnobs(knobs)
```
(The downsample emitters re-resolve via `resolveTransformCodec(cvCodec, cvQuality)`
for the actual `fac`/`knobs`; this `quality` int is the fallback/Aperio-token value
— now codec-aware. Confirm the emitters use `resolveTransformCodec`'s returned knobs
for encoding, and this int only for the `Q=` token / SVS-jpeg fallback.)

For `cropToSVS`/`runCrop`: the crop quality int default (used in the Aperio token)
should likewise be `qFromKnobs(codecDefaultKnobs(codec))` when `--quality` absent
(85), not a hardcoded 90. (cropEmitSVS→cropToSVS used `quality==0 → 90`; change that
default path to 85 via `qFromKnobs`, or simpler: `quality = 85` — but prefer routing
through `qFromKnobs(codecDefaultKnobs(p.codecName))` for the source-of-truth.)

- [ ] **Step 4: Build + test; update q90 baselines to q85**

`go build ./cmd/wsitools/ && go test ./cmd/wsitools/ -run 'Codec|Quality|Convert|Crop|Downsample|Aperio' -count=1`. Update any test asserting q90/Q≈88 on a
default re-encode to q85/Q≈83 (intended). Report which. `gofmt -l` clean.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
feat(codec): absent --quality uses the per-codec default (not forced 90)

resolveTransformCodec seeds knobs from codecDefaultKnobs (q85 / jxl
distance-1.0) when --quality is absent; parseQualityKnobs default 90→85;
callers derive the int quality via qFromKnobs. Fixes jpegxl (was q90→1.5,
now distance-1.0); jpeg re-encode default 90→85. Explicit --quality wins.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Integration gate (controller-run)

- [ ] **Step 1: Build** — `make build`. `SRC=sample_files/svs/CMU-1-Small-Region.svs`

- [ ] **Step 2: jpegxl default is distance-1.0, not q-mapped-1.5** — write a JXL
TIFF with no `--quality` and confirm it decodes (and, if a distance is inspectable,
that it's the native default, not 1.5):
```bash
./bin/wsitools convert --to tiff --codec jpegxl -o /tmp/q-jxl.tiff "$SRC" 2>&1 | tail -1
./bin/wsitools hash --mode pixel /tmp/q-jxl.tiff >/dev/null && echo "jxl default decodes"
```

- [ ] **Step 3: jpeg default is now q85** (was q90):
```bash
./bin/wsitools convert --to tiff --factor 2 -o /tmp/q-ds.tiff "$SRC"
./bin/wsitools info /tmp/q-ds.tiff | grep -iE "Q≈8"   # Q≈83 (q85), not Q≈88 (q90)
./bin/wsitools convert --to svs --rect 0,0,2048,2048 -o /tmp/q-svs.svs "$SRC"
./bin/wsitools dump-ifds --raw /tmp/q-svs.svs 2>/dev/null | grep -i ImageDescription | head -1 | grep -o "Q=85"
```

- [ ] **Step 4: crop ≡ downsample parity holds at q85; explicit --quality wins**:
```bash
./bin/wsitools convert --to tiff --factor 2 -o /tmp/q-A.tiff "$SRC"; ./bin/wsitools convert --to tiff --rect 0,0,2220,2967 --factor 2 -o /tmp/q-B.tiff "$SRC"
./bin/wsitools hash --mode pixel /tmp/q-A.tiff; ./bin/wsitools hash --mode pixel /tmp/q-B.tiff   # MATCH
./bin/wsitools convert --to tiff --factor 2 --quality 95 -o /tmp/q-95.tiff "$SRC"; ./bin/wsitools info /tmp/q-95.tiff | grep -iE "Q≈9"  # explicit 95 honored
```

- [ ] **Step 5: reversible J2K still lossless**:
```bash
./bin/wsitools convert --to tiff --factor 2 --codec jpeg2000 --quality reversible=true -o /tmp/q-rev.tiff "$SRC" 2>&1 | tail -1; echo "exit=$?"
```

- [ ] **Step 6: Clean up** `/tmp/q-*`.

---

## Self-review

**Spec coverage:** `codecDefaultKnobs` + `qFromKnobs` (Task 1); resolve seed +
parseQualityKnobs revert + caller int (Task 2); jxl/jpeg-default + parity + explicit
override (Task 3). **Type consistency:** `codecDefaultKnobs(string)
map[string]string`, `qFromKnobs(map[string]string) int`,
`resolveTransformCodec(codecName, quality string)` (drops `fallbackQ`) — all call
sites updated. **Behavior change:** default re-encode q90→q85 (q-codecs), jxl→1.0;
explicit `--quality` unchanged.

## Boundaries

**In:** the per-codec default map + wiring. **Deferred:** per-codec non-default
tuning; first-class codec-knob CLI flags.
