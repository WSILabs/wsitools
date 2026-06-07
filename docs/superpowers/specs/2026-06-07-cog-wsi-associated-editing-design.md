# COG-WSI associated-image editing (Slice 2a) — design

> Status: **approved design** (brainstormed 2026-06-07). Next: writing-plans.
> Branch off `main`; never implement on `main`.
> Extends the associated-image editing feature (Slice 1: SVS + generic-TIFF) to
> COG-WSI. OME-TIFF (Slice 2b) gets its own spec.

## Goal

Make `wsitools label|macro|thumbnail|overview remove|replace` work on **COG-WSI**
files, honoring the same contract as Slice 1: **only the targeted associated
image changes; everything else is preserved.** For COG-WSI this is done by
re-finalizing the file through `cogwsiwriter` (the splice engine used for
SVS/generic-TIFF cannot be used here — see Background).

## Background

### Why not the Slice 1 splice engine
Slice 1 edits SVS/generic-TIFF in place via a prefix-copy + tail-re-emit splice:
the pyramid is copied byte-for-byte and only the target's tail IFD is dropped.
COG-WSI has a **clean linear top-level chain** (IFD0 = pyramid `WSIImageType=pyramid`;
thumbnail/label/overview are trailing top-level IFDs with `WSIImageType` tags;
associated-image data lives at the file tail), so it *looks* splice-friendly — but
COG-WSI is a **strict format with global invariants** a splice would silently
break:
- a **ghost area** right after the header (GDAL convention) that lets a COG
  reader locate data without walking IFDs — splicing an IFD in/out leaves it
  stale;
- tile data in **reverse IFD order**, monotonically increasing offsets within a
  level, and **16-byte-aligned** tile offsets — the splice rebases offsets by
  plain concatenation with 2-byte alignment, honoring none of these;
- `replace` inserts bytes, shifting the tail and forcing offset/alignment/ghost
  updates the splice doesn't manage.

Only `cogwsiwriter` knows these rules, so the conformance-safe approach is to
**re-finalize through it**.

### Why this is bounded (fidelity already wired)
The full-fidelity contract requires carrying everything except the target image.
For COG-WSI that is largely already true of the `convert --to cog-wsi` path:
- **pyramid pixels are preserved verbatim** — verified: `convert --to cog-wsi`
  on `cog-wsi/CMU-1-Small-Region_cog-wsi.tiff` produces an identical
  `hash --mode pixel` (raw compressed tiles copied, no re-encode);
- **associated images survive** the convert;
- **ICC is already carried**: `cogwsiwriter` emits tag 34675
  (`internal/tiff/cogwsiwriter/icc_test.go`), `convert_cogwsi.go:69` passes
  `md.ICCProfile`, `source.Metadata.ICCProfile` exposes it;
- **MPP / magnification / resolution / provenance ImageDescription** are carried
  by the existing `convert_cogwsi` metadata path.
- COG-WSI is *our* clean format, so there are **no foreign vendor tags** to lose.

So Slice 2a is essentially `convert --to cog-wsi` parameterized with an
associated-edit plan, plus output-path/`--in-place` handling and tests.

## Scope (decided)

- **Format:** COG-WSI only (OME-TIFF = Slice 2b).
- **Ops:** `remove` and `replace` (upsert), for all four types
  (`label`/`macro`/`thumbnail`/`overview`).
- **Contract:** only the target image changes; pyramid pixels, all other
  associated images, MPP/mag/resolution/ICC/provenance preserved.
- **Out of scope:** OME-TIFF; the SVS abbreviated-JPEG associated encoder
  (irrelevant — COG-WSI uses self-contained JPEG).

## Architecture

### Command surface (no new commands)
The Slice 1 commands (`<type> remove|replace`) gain COG-WSI by changing the
format dispatch in `cmd/wsitools/associated.go`:

```
src.Format():
  "svs" | "generic-tiff" → splice engine      (Slice 1, unchanged)
  "cog-wsi"              → rebuild engine      (this slice)
  otherwise              → ErrUnsupportedAssoc (ome-tiff: "coming next")
```

All shared flags/behavior carry over unchanged: `-o/--output`, `--in-place`
(temp + fsync + rename), `--overwrite`, `--fsync`, `-q`; and for `replace`:
`--image` (required), `--compression`, `--resize` (default `fit`), `--bg`
(default `F5F5E6`), `--label-dims`, `--force`. Output defaults to
`<stem>_relabeled<ext>`.

### Edit plan
```go
type assocEditPlan struct {
	remove  string                       // type to drop ("" = none)
	replace string                       // type to substitute/append ("" = none)
	spec    *cogwsiwriter.AssociatedSpec // the replacement (when replace != "")
	dropAll bool                         // write no associated images (convert's --no-associated)
}
```
At most one of `remove`/`replace` is set per invocation; the empty plan
(`assocEditPlan{}`) writes all associated images verbatim, and `dropAll` writes
none. `convert --to cog-wsi` passes `assocEditPlan{}` normally and
`assocEditPlan{dropAll: true}` when `--no-associated` is set — so its behavior is
unchanged.

### Rebuild engine (`cmd/wsitools/associated_rebuild.go`)
`runAssociatedRemoveForCOGWSI` / `runAssociatedReplaceForCOGWSI` call a shared
`rebuildCOGWSI(src source.Source, outPath string, plan assocEditPlan, fsync bool) error`:

1. `cogwsiwriter.Create(outPath, opts)` with `Metadata` carrying MPP/mag/
   resolution/ICC/provenance from `src.Metadata()` (same as `convert_cogwsi`).
2. **Pyramid:** for each `src.Levels()` level, `AddLevel` + copy every tile via
   `TileInto` → `WriteTile` (verbatim compressed bytes — the proven lossless
   path).
3. **Associated images:** for each `a` in `src.Associated()`:
   - if `a.Type() == plan.remove` → skip it;
   - if `a.Type() == plan.replace` → `AddAssociated(*plan.spec)` instead of the
     source bytes;
   - else → `AddAssociated` with the source bytes (verbatim), as today.
   - For `replace` when the type is **absent** (upsert): after the loop, if the
     target wasn't seen, `AddAssociated(*plan.spec)`.
4. `w.Close()` (or `w.Abort()` on any error — no partial output).
5. **Atomic output:** `rebuildCOGWSI` always creates the writer at a sibling temp
   path (`<outPath>.tmp-<pid>`), then `os.Rename`s over `outPath` on success
   (removing the temp on any error). This makes both `-o` and `--in-place`
   (`outPath == input`) safe — a crash mid-build never corrupts the target.
   `--fsync` syncs the temp before rename.

### Shared finalize core (small `convert_cogwsi` refactor)
Steps 1–4 duplicate `runConvertCOGWSI` (`convert_cogwsi.go:79–147`). Extract the
level-copy + associated-write loops into a reusable
`writeCOGWSI(w *cogwsiwriter.Writer, src source.Source, plan assocEditPlan) error`.
Both `convert --to cog-wsi` and the rebuild engine call it — one byte-exact loop,
no drift. `convert` passes `assocEditPlan{}` (or `{dropAll:true}` for
`--no-associated`), keeping its behavior identical (regression net = existing
convert tests).

### Replacement → `AssociatedSpec` (`replace`)
Reuse Slice 1's image helpers in `associated_replace.go` (decode PNG/JPEG/TIFF,
`fitImage` resize/letterbox, aspect guard, per-type codec default
label→LZW / others→JPEG, `--compression` override, strip encoders). Package the
result as `cogwsiwriter.AssociatedSpec{Type, Width, Height, Compression,
Photometric:2, Bytes}` rather than Slice 1's `edit.ReplacementIFD`. Target dims:
existing image's `Size()` if present, else `--label-dims`, else per-type default.
Classification is correct automatically via `AssociatedSpec.Type` (no SVS-style
`NewSubfileType` concern). COG-WSI associated images are self-contained
JPEG/LZW, so a JPEG replacement round-trips — no abbreviated-JPEG limitation.

## Data flow

```
remove:
  source.Open(cog-wsi) → Metadata + Levels + Associated
  plan{remove: typ}
  rebuildCOGWSI → cogwsiwriter (verbatim tiles; assoc minus target; carry meta)
  → outPath (or temp→rename for --in-place)

replace:
  source.Open; locate existing assoc of typ (for dims) — absent is OK (upsert)
  decode --image → resize/letterbox → encode → cogwsiwriter.AssociatedSpec
  plan{replace: typ, spec}
  rebuildCOGWSI (verbatim tiles; assoc with target substituted/appended)
  → outPath
```

## Error handling

| Condition | Behavior |
|---|---|
| `remove`, type absent | error `no <type> image in slide` |
| `replace`, `--image` missing/undecodable | error |
| unsupported `--compression` | error |
| `-o` and `--in-place` both set | error |
| resolved output == input (non-in-place) | error |
| aspect mismatch >2× without `--force` | error |
| writer error mid-build | `w.Abort()`, no partial output |

## Testing

`make test` (`-race -count=1`); integration gated by `WSI_TOOLS_TESTDIR`.
Fixtures: `cog-wsi/CMU-1_cog-wsi.tiff` and `cog-wsi/CMU-1-Small-Region_cog-wsi.tiff`
(both carry thumbnail+label+overview).

**Integration (gated):**
- `label remove` ⇒ output reopens as `cog-wsi` (conformant); `label` absent from
  `src.Associated()`; **thumbnail + overview still present**; **pyramid
  `hash --mode pixel` identical** to source; MPP/mag/ICC preserved (compare
  `source.Metadata()` fields in/out).
- `overview replace --image <png>` ⇒ output has `overview` of the expected dims;
  `src.Associated()` overview `Bytes()` decodes via `image.Decode`; type still
  `overview`; pyramid hash identical; other associated images unchanged.
- `--in-place` remove ⇒ original path edited, no temp leftover, pyramid identical.
- `remove` of an absent type ⇒ `no <type> image in slide` error.
- **convert non-regression:** existing `convert --to cog-wsi` tests still pass
  after the `writeCOGWSI` extraction (byte/pixel-exact as before).

**Unit:**
- edit-plan application (skip / substitute / append) over a small fake
  associated set.
- replacement→`AssociatedSpec` packaging (type, dims, compression tag).

## File structure

| Path | Responsibility |
|---|---|
| `cmd/wsitools/associated.go` (modify) | dispatch: cog-wsi → rebuild engine |
| `cmd/wsitools/associated_rebuild.go` (new) | `rebuildCOGWSI` + per-type remove/replace entry points + `assocEditPlan` |
| `cmd/wsitools/convert_cogwsi.go` (modify) | extract shared `writeCOGWSI(w, src, plan)`; `convert` calls it with an empty plan |
| `cmd/wsitools/associated_replace.go` (reuse) | image decode/resize/encode helpers; add an `AssociatedSpec` packager |
| `cmd/wsitools/associated_rebuild_test.go` (new) | unit: plan application + spec packaging |
| `cmd/wsitools/associated_integration_test.go` (extend) | gated cog-wsi remove/replace/in-place |
| `README.md`, `CHANGELOG.md`, `docs/roadmap.md` | matrix cell cog-wsi ✓; Slice 2b = OME-TIFF |

## Out of scope (Slice 2b / later)

- **OME-TIFF** associated editing — its own spec: raw IFD-graph re-serializer
  (SubIFD trees, offset aliasing), OME-XML `<Image>` surgery + OME-XML
  relocation when the removed image is IFD0, verbatim vendor-tag carry.
- `--rotate`, `--if-exists` (deferred from Slice 1).

## Open questions

None blocking. The OME-TIFF graph re-serializer (Slice 2b) is the known larger
follow-up and is deliberately excluded.
