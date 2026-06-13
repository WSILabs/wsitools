package main

import (
	"fmt"
	"strconv"
	"strings"
)

// AperioDescription represents a parsed Aperio SVS ImageDescription tag (270).
// Format reference: opentile-go's formats/svs/metadata.go (the canonical reader).
//
// Wire format:
//
//	<SoftwareLine>\r\n<W>x<H> [...] <details>|key1 = value1|key2 = value2|...
//
// Parsing strategy: line 1 = software banner; everything after \n joined by
// pipes. The first pipe-separated chunk is the geometry+codec banner; subsequent
// chunks are key=value pairs.
//
// These helpers live caller-side (cmd/wsitools) because the Aperio-specific
// ImageDescription text format is a property of the SVS container, not of TIFF
// byte emission. They were previously co-located with the now-deleted
// internal/wsiwriter package.
type AperioDescription struct {
	SoftwareLine  string            // e.g. "Aperio Image Library v12.0.15"
	GeometryLine  string            // e.g. "46000x32914 [0,100 46000x32814] (240x240) JPEG/RGB Q=70"
	AppMag        float64           // mutated on downsample
	MPP           float64           // mutated on downsample
	Properties    map[string]string // all key=value pairs verbatim
	PropertyOrder []string          // preserve original order for round-tripping
}

func ParseImageDescription(desc string) (*AperioDescription, error) {
	if !strings.HasPrefix(desc, "Aperio") {
		return nil, fmt.Errorf("svs: not an Aperio ImageDescription")
	}
	desc = strings.ReplaceAll(desc, "\r\n", "\n")
	lines := strings.SplitN(desc, "\n", 2)
	if len(lines) < 2 {
		return nil, fmt.Errorf("svs: malformed Aperio ImageDescription (no second line)")
	}
	d := &AperioDescription{
		SoftwareLine: lines[0],
		Properties:   map[string]string{},
	}
	chunks := strings.Split(lines[1], "|")
	d.GeometryLine = chunks[0]
	for _, c := range chunks[1:] {
		eq := strings.Index(c, "=")
		if eq < 0 {
			continue
		}
		k := strings.TrimSpace(c[:eq])
		v := strings.TrimSpace(c[eq+1:])
		if _, dup := d.Properties[k]; !dup {
			d.PropertyOrder = append(d.PropertyOrder, k)
		}
		d.Properties[k] = v
	}
	if v, ok := d.Properties["AppMag"]; ok {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, fmt.Errorf("svs: AppMag parse: %w", err)
		}
		d.AppMag = f
	}
	if v, ok := d.Properties["MPP"]; ok {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, fmt.Errorf("svs: MPP parse: %w", err)
		}
		d.MPP = f
	}
	return d, nil
}

// MutateForDownsample updates AppMag, MPP, and the geometry line for a
// power-of-2 downsample factor. newW and newH are the L0 dimensions of the
// downsampled output (source dimensions / factor).
func (d *AperioDescription) MutateForDownsample(factor int, newW, newH uint32) {
	d.AppMag = d.AppMag / float64(factor)
	d.MPP = d.MPP * float64(factor)
	d.Properties["AppMag"] = formatAperioFloat(d.AppMag)
	d.Properties["MPP"] = formatAperioFloat(d.MPP)
	parts := strings.SplitN(d.GeometryLine, " ", 2)
	if strings.Contains(parts[0], "x") {
		newGeo := fmt.Sprintf("%dx%d", newW, newH)
		if len(parts) == 2 {
			d.GeometryLine = newGeo + " " + parts[1]
		} else {
			d.GeometryLine = newGeo
		}
	}
	if _, ok := d.Properties["OriginalWidth"]; ok {
		d.Properties["OriginalWidth"] = fmt.Sprintf("%d", newW)
	}
	if _, ok := d.Properties["OriginalHeight"]; ok {
		d.Properties["OriginalHeight"] = fmt.Sprintf("%d", newH)
	}
}

// Encode reconstructs the Aperio ImageDescription string in wire format.
func (d *AperioDescription) Encode() string {
	var b strings.Builder
	b.WriteString(d.SoftwareLine)
	b.WriteString("\r\n")
	b.WriteString(d.GeometryLine)
	for _, k := range d.PropertyOrder {
		b.WriteString("|")
		b.WriteString(k)
		b.WriteString(" = ")
		b.WriteString(d.Properties[k])
	}
	return b.String()
}

func formatAperioFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// Quality extracts the JPEG quality (the "Q=<n>" token) from the geometry line,
// e.g. "... JPEG/RGB Q=30". Returns ok=false if absent or unparseable.
func (d *AperioDescription) Quality() (int, bool) {
	i := strings.Index(d.GeometryLine, "Q=")
	if i < 0 {
		return 0, false
	}
	rest := d.GeometryLine[i+2:]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	q, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0, false
	}
	return q, true
}

// BuildCropImageDescription constructs the ImageDescription (tag 270) for a
// crop, following Aperio ImageScope's recipe (docs/aperio-svs-crop-analysis.md):
//
//	Aperio Image Library v<wsitools-version>
//	<baseW>x<baseH> [x,y cropWxcropH] (tileWxtileH) JPEG/RGB Q=q;<SOURCE-DESC-VERBATIM>|OriginalWidth = baseW|OriginalHeight = baseH
//
// The new header line keeps the literal "Aperio Image Library v" prefix so
// opentile-go's SVS detector (matchSVS: HasPrefix "Aperio") recognizes the
// output. The entire source description is appended verbatim after the ';'
// (the provenance chain), so MPP/AppMag/ImageID/Left/Top and all scanner fields
// are preserved unchanged. A fresh OriginalWidth/OriginalHeight pair (the
// pre-crop base dims) is appended at the end.
func BuildCropImageDescription(srcDesc string, baseW, baseH, x, y, cropW, cropH, tileW, tileH, quality int) string {
	chain := strings.ReplaceAll(srcDesc, "\r\n", "\n")
	var b strings.Builder
	b.WriteString("Aperio Image Library v")
	b.WriteString(Version)
	b.WriteString("\r\n")
	fmt.Fprintf(&b, "%dx%d [%d,%d %dx%d] (%dx%d) JPEG/RGB Q=%d;", baseW, baseH, x, y, cropW, cropH, tileW, tileH, quality)
	b.WriteString(chain)
	fmt.Fprintf(&b, "|OriginalWidth = %d|OriginalHeight = %d", baseW, baseH)
	return b.String()
}

// SyntheticAperioDescription builds an Aperio-shaped ImageDescription
// for SVS output written by wsitools from a non-SVS source. Follows
// the third-party-vendor convention (e.g. Grundium's "Aperio Image,
// Grundium Ocus"): keep the literal "Aperio" prefix so opentile-go's
// detection passes, append the wsitools provenance after a comma.
//
// MPP / AppMag are emitted only when src.Metadata() provides them
// (non-zero); a missing key is preferable to a fake value.
func SyntheticAperioDescription(l0W, l0H, tileW, tileH uint32, quality int, mpp, appMag float64, srcSoftware string) *AperioDescription {
	soft := "Aperio Image, wsitools/" + Version
	if srcSoftware != "" {
		soft += " (from " + srcSoftware + ")"
	}
	geom := fmt.Sprintf("%dx%d (%dx%d) JPEG/RGB Q=%d", l0W, l0H, tileW, tileH, quality)
	d := &AperioDescription{
		SoftwareLine: soft,
		GeometryLine: geom,
		Properties:   map[string]string{},
	}
	if appMag != 0 {
		d.AppMag = appMag
		d.Properties["AppMag"] = formatAperioFloat(appMag)
		d.PropertyOrder = append(d.PropertyOrder, "AppMag")
	}
	if mpp != 0 {
		d.MPP = mpp
		d.Properties["MPP"] = formatAperioFloat(mpp)
		d.PropertyOrder = append(d.PropertyOrder, "MPP")
	}
	return d
}
