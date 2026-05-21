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
