# Downsample-Kernel Audit Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to walk this audit step by step. This is a research-then-maybe-fix task, not a feature build — the conclusion may be "no change needed."

**Goal:** Determine whether wsitools' current downsample kernel choices (2×2 box average everywhere) are right for image quality, by characterizing them against libvips defaults and eye-testing tissue images side-by-side.

**Architecture:** Pure investigation. No code changes until the data justifies them. If a change lands, it's narrowly scoped: either flip a default, expose a `--kernel` flag, or both.

**Tech Stack:** Comparison against libvips (already installed for the `make bench-dzi` benchmark target).

**Background:** v0.17 picked 2×2 box averaging for DZI cascade on perf grounds (Lanczos at the top of cascade was 80% of CPU). After last conversation we discovered libvips `dzsave` also uses 2×2 box averaging (`--region-shrink=mean` is the default) — so wsitools and libvips are at parity for DZI. The interesting question is **`downsample` CLI**, which uses box-halving everywhere and has no libvips parity claim — libvips' general-purpose resize defaults to Lanczos3.

---

## Scope split

The audit is two independent investigations. Either can land on its own.

**Part A: DZI cascade kernel.** Verify the libvips-parity claim. If wsitools and libvips dzsave really both default to 2×2 box average, we're done — close the TODO with "no change, parity confirmed." If libvips actually uses something else (the docs I'm reading may be stale or wrong), revisit.

**Part B: `downsample` CLI kernel.** Where libvips equivalents (`vipsthumbnail`, `vips_resize`) default to Lanczos3 and wsitools uses box. Tissue images downsampled by Lanczos3 typically look sharper than box-averaged ones at the same scale; whether that visible difference matters for the `downsample` CLI's use case (slide-to-slide magnification reduction, not viewer-grade rendering) is the question.

Each part has its own checklist below.

---

## Part A: Verify DZI cascade kernel matches libvips

**Goal:** Confirm or refute the "wsitools DZI cascade uses the same downsample algorithm as libvips dzsave" claim.

- [ ] **Step A1: Read libvips dzsave source to nail the default**

Pull the libvips source for `vips_foreign_save_dz_file_build` and `vips_region_shrink_method`. Find what kernel `region_shrink` defaults to and how it's applied across pyramid levels.

Run:
```sh
brew --prefix libvips
# inspect Cellar/libvips/<ver>/share/man/man1/vips-dzsave.1 for documented default
# inspect github.com/libvips/libvips/blob/master/libvips/foreign/dzsave.c if not in Cellar
```

Expected outcome: confirms or refutes that `--region-shrink=mean` is the default and that "mean" is the 2×2 box average.

- [ ] **Step A2: Run libvips dzsave on CMU-1.ndpi**

```sh
vips dzsave sample_files/ndpi/CMU-1.ndpi /tmp/libvips-cmu1-dzi --suffix '.jpeg[Q=85]' --tile-size 256 --overlap 1
```

Expected output: `/tmp/libvips-cmu1-dzi.dzi` + `/tmp/libvips-cmu1-dzi_files/<level>/<col>_<row>.jpeg`.

- [ ] **Step A3: Run wsitools convert --to dzi on the same input**

```sh
./bin/wsitools convert --to dzi -o /tmp/wsitools-cmu1.dzi sample_files/ndpi/CMU-1.ndpi
```

- [ ] **Step A4: Compare pyramid level structure**

```sh
diff <(ls /tmp/libvips-cmu1-dzi_files/) <(ls /tmp/wsitools-cmu1.dzi_files/)
diff <(cat /tmp/libvips-cmu1-dzi.dzi) <(cat /tmp/wsitools-cmu1.dzi)
```

Expected: same number of levels, same per-level tile counts, same tile dimensions. Manifest XML may differ in attributes (`Format`, version), but pyramid shape should match.

- [ ] **Step A5: Eye-test a mid-pyramid tile from each**

Pick a tile at L_max-3 or L_max-4 (small enough to see slide-level features, large enough that codec differences are observable). Open both in macOS Preview or Quick Look:

```sh
open /tmp/libvips-cmu1-dzi_files/10/12_8.jpeg /tmp/wsitools-cmu1.dzi_files/10/12_8.jpeg
```

(Substitute the actual level + tile coords; the example assumes L_max≈13 and a center tile.)

- [ ] **Step A6: Decide**

Three outcomes:
1. **Libvips also uses box averaging** → write up the parity finding, close the TODO with "no change."
2. **Libvips uses something else and tiles look meaningfully different** → proceed to Part C below.
3. **Libvips uses something else but tiles look identical** → write up the finding, close with "parity in observed output even though algorithms differ on paper."

- [ ] **Step A7: Commit the Part A writeup**

Save findings to `docs/notes/2026-05-29-dzi-kernel-audit.md` (new file). Commit message: `docs(notes): DZI cascade kernel parity check vs libvips`.

---

## Part B: `downsample` CLI kernel quality

**Goal:** Determine whether `downsample` CLI's box-only kernel produces visibly worse output than Lanczos3, and whether that matters for the CLI's use cases.

- [ ] **Step B1: Pick a tissue-rich source fixture**

Use `sample_files/svs/CMU-1-Small-Region.svs` (small, fast, has visible tissue structure). If quality differences are subtle on CMU-1-Small, escalate to a full CMU-1 SVS for the final eye test.

- [ ] **Step B2: Generate the wsitools box-halving output**

```sh
./bin/wsitools downsample --factor 4 -o /tmp/wsitools-box.svs sample_files/svs/CMU-1-Small-Region.svs
```

- [ ] **Step B3: Generate a libvips Lanczos3 equivalent**

libvips' `vipsthumbnail` does scale-to-fit thumbnailing, not factor-2 SVS regeneration. Use `vips resize` with explicit factor:

```sh
vips resize sample_files/svs/CMU-1-Small-Region.svs /tmp/libvips-lanczos.tif 0.25 --kernel lanczos3
```

(Note: this produces a flat TIFF, not a pyramidal SVS. For the eye test, both files' L0 is what we compare.)

- [ ] **Step B4: Extract L0 from each as PNG for diffing**

```sh
./bin/wsitools region --x 0 --y 0 --w 1024 --h 1024 --level 0 -o /tmp/wsitools-region.png /tmp/wsitools-box.svs
vips crop /tmp/libvips-lanczos.tif /tmp/libvips-region.png 0 0 1024 1024
```

- [ ] **Step B5: Pixel-diff and eye-test**

```sh
# Sum of absolute differences (lower = more similar)
python3 -c "
import PIL.Image, numpy
a = numpy.array(PIL.Image.open('/tmp/wsitools-region.png')).astype(int)
b = numpy.array(PIL.Image.open('/tmp/libvips-region.png')).astype(int)
print('mean abs diff:', numpy.abs(a-b).mean())
print('max abs diff:', numpy.abs(a-b).max())
"
open /tmp/wsitools-region.png /tmp/libvips-region.png
```

Look specifically at:
- Cell membrane sharpness — Lanczos3 should be visibly crisper than box at the same scale.
- Aliasing artefacts — box averaging can introduce blocky transitions on diagonal features.
- Overall perceived "softness" — box looks slightly smudgy, Lanczos3 looks more like the original.

- [ ] **Step B6: Decide**

Three outcomes:
1. **Mean abs diff < ~2 and no visible difference** → keep box default, close with "no change needed."
2. **Visible difference + use-case justifies it** (e.g., the downsampled slide will be re-viewed by pathologists, not just used as a smaller archive) → flip default to Lanczos3, or expose `--kernel {box,lanczos3}` with box as default.
3. **Visible difference but use-case doesn't care** (archival, file-size reduction) → keep box default, document the tradeoff.

The "use-case justifies it" call is a product decision, not a benchmark. Ask the user.

### CLI flag design (for Step B7)

If the eye test lands on outcome 2, expose `--kernel`. Naming choice and value set below are pre-decided so the audit phase doesn't drift into bikeshedding.

**Flag name:** `--kernel`. Matches libvips' `vips resize --kernel <name>` and is what users searching for "what kernel does libvips use" will already expect.

**Value set:** four kernels — exactly the set opentile-go currently exposes (`internal/decoder` + `opentile-go/resample` define `Nearest`, `Bilinear`, `Lanczos`, `Box`). opentile-go's `Lanczos` is 3-lobe (verified: `resample/lanczos.go:9` has `lanczosA = 3.0`), so it maps cleanly to libvips' `lanczos3`:

| `--kernel` value | opentile-go enum | libvips name | Notes |
|---|---|---|---|
| `lanczos3` | `resample.Lanczos` | `lanczos3` | **Default for `downsample` CLI** — matches libvips general-resize default. |
| `box` | `resample.Box` | (no direct equiv as resize kernel; `region-shrink=mean` for dzsave) | **Default for `convert --to dzi\|szi`** — matches libvips dzsave default; no change from today. |
| `linear` | `resample.Bilinear` | `linear` | Alias accepted: `bilinear`. |
| `nearest` | `resample.Nearest` | `nearest` | Fast but ugly; debug aid. |

**Defaults differ by command, intentionally.** The flag itself is shared (same name, same value set, same mapping) but each subcommand picks its own default to match libvips' equivalent tool:

- `downsample` defaults to `lanczos3` (libvips `vips resize` default).
- `convert --to dzi|szi` defaults to `box` (libvips `vips dzsave --region-shrink=mean` default).
- `convert --to svs|tiff|ome-tiff` — no scaling happens unless source pyramid level matches output dimensions, so the flag is a no-op. Skip wiring there.

**Help text** must explain the per-command default so users aren't surprised by `downsample --kernel` and `convert --to dzi --kernel` having different defaults. Suggested phrasing for `downsample`:

```
--kernel string   Resample kernel for pyramid halving:
                  lanczos3 (default; sharp, matches libvips)
                  box      (fast, less sharp; matches libvips dzsave)
                  linear   (intermediate; same as 'bilinear')
                  nearest  (debug; visible aliasing)
```

**Unsupported libvips kernels (`cubic`, `mitchell`, `lanczos2`)**: not exposed. If a user asks for one, error with `unknown kernel "<name>" — accepted: lanczos3, box, linear, nearest`. Adding them is a separate opentile-go contribution (new kernel implementations in `opentile-go/resample/`), not in scope for this audit.

**Alias handling:** `bilinear` is accepted as a synonym for `linear` (Go ecosystem convention). All other names match libvips exactly. Implement aliases in the flag parser, not via cobra (cobra doesn't have first-class alias support for enum values).

### Step B7: Impl skeleton

Only proceed here if Step B6 lands on outcome 2.

- Add `--kernel string` flag to `cmd/wsitools/downsample.go` cobra flags. Default `"lanczos3"`.
- (Optional, can defer) Add the same flag to `cmd/wsitools/convert.go` for the dzi/szi target. Default `"box"`.
- Write a small `parseKernel(s string) (otresample.Kernel, error)` helper in `cmd/wsitools/kernel.go` (new file) that maps the flag value to the opentile-go enum, handles the `bilinear` alias, and errors on unknown values.
- Wire `parseKernel(flagValue)` into the `otresample.ImageInto` calls at `downsample.go:488` and `downsample.go:680`.
- Add `TestParseKernel` (table-driven unit test for the mapping) + `TestDownsampleKernelFlag` (integration test that runs `downsample --kernel lanczos3` and `downsample --kernel box` on CMU-1-Small-Region.svs and asserts the L0 raster bytes differ, proving the flag is wired through).
- Update README's downsample examples to show `--kernel lanczos3` and the new default.
- Update CHANGELOG with the new flag + the default-change for `downsample`. Note the default change is a behavior change for existing users who relied on box-averaged output bit-equivalence between releases — flag it prominently.
- v0.21 (or whatever the next release is) is a minor bump because the default output changes.

Full implementation plan to get written as a separate plans/ doc at that point if Step B6 escalates. Not pre-spelled out here because there's a real chance Steps B1-B6 conclude "no change needed."

- [ ] **Step B8: Commit the Part B writeup**

Append findings to `docs/notes/2026-05-29-dzi-kernel-audit.md` (the same notes file from Part A, under a new section). Commit message: `docs(notes): downsample CLI kernel quality vs libvips`.

---

## Part C (conditional): Replace box with something else in DZI cascade

**Only execute if Part A Step A6 lands on outcome 2 (libvips uses something else AND tiles look meaningfully different).**

Not pre-spelled out. If we get here, write a fresh implementation plan covering:
- Kernel change in `boxDownsample2x` (rename + reimplement) or via the `opentile-go/resample` package.
- Perf regression check on CMU-1.ndpi (must stay under 30s end-to-end for the CMU-1 fixture).
- Quality A/B fixture in tests.

---

## Updates to existing docs (regardless of audit outcome)

These can land before the audit completes — they correct the framing of the current TODO.

- [ ] **Step D1: Update roadmap.md TODO entry**

Replace the v0.20-added "Revisit downsample algorithm choice for DZI creation" entry with a more accurate one. New text:

```markdown
### Deferred from v0.20 (added 2026-05-29)
- **Downsample kernel audit.** wsitools currently uses 2×2 box averaging for both `convert --to dzi|szi` cascade and `downsample` CLI factor-2 chain. libvips `dzsave` uses the same (`--region-shrink=mean`), so the DZI cascade is at parity. The interesting question is `downsample` CLI, where libvips' general-purpose resize defaults to Lanczos3 and we don't — audit whether the quality difference is visible on tissue slides and whether the use case justifies it. See `docs/superpowers/plans/2026-05-29-downsample-kernel-audit.md`.
```

- [ ] **Step D2: Update memory entry**

Update `~/.claude/projects/-Users-cornish-GitHub-wsitools/memory/project_dzi_kernel_revisit.md` to reflect the libvips-parity finding and the reframed Part A / Part B / Part C structure. Specifically: drop the "v0.17 was perf-motivated, revisit quality" framing (still true but incomplete) and add the "libvips uses the same algorithm for DZI cascade" finding.

- [ ] **Step D3: Commit the framing fixes**

```bash
git add docs/roadmap.md docs/superpowers/plans/2026-05-29-downsample-kernel-audit.md
git commit -m "docs(roadmap): refine kernel audit framing; libvips dzsave uses box too"
```

(Memory file is not in the repo; updated separately.)

---

## Self-review

**Spec coverage:** the original ask ("revisit downsample algorithm choice for DZI creation") is covered by Part A. Part B extends the audit to `downsample` CLI where the question is more substantive. Part C is the conditional fix path.

**Placeholders:** none. Every step has concrete commands.

**Type consistency:** N/A (no new types defined; existing `otresample.Kernel`, `boxDownsample2x` referenced match the source).

**Risk of scope creep:** Part B could expand into a "let's add a kernel flag to every CLI" project. Resist. Only flip a default or expose a flag if eye-test data justifies it for the specific path being audited.
