package cogwsiwriter

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
)

// COGWSIVersion is the format version this writer emits.
const COGWSIVersion = "0.1"

// Ghost is the COG-WSI ghost area written immediately after the TIFF header.
// See docs/superpowers/specs/2026-05-20-cog-wsi-format.md §4.
type Ghost struct {
	Layout                   string // e.g. "IFDS_BEFORE_DATA"
	BlockOrder               string // e.g. "ROW_MAJOR"
	BlockLeader              string // e.g. "SIZE_AS_UINT4"
	BlockTrailer             string // e.g. "LAST_4_BYTES_REPEATED"
	KnownIncompatibleEdition string
	Version                  string // COG_WSI_VERSION
}

func defaultGhost() Ghost {
	return Ghost{
		Layout:                   "IFDS_BEFORE_DATA",
		BlockOrder:               "ROW_MAJOR",
		BlockLeader:              "SIZE_AS_UINT4",
		BlockTrailer:             "LAST_4_BYTES_REPEATED",
		KnownIncompatibleEdition: "NO",
		Version:                  COGWSIVersion,
	}
}

// Marshal serializes the ghost area. The first line's size value is the
// byte length of everything after the size line's terminating newline,
// in six ASCII digits per GDAL convention.
func (g Ghost) Marshal() ([]byte, error) {
	body := fmt.Sprintf(
		"LAYOUT=%s\nBLOCK_ORDER=%s\nBLOCK_LEADER=%s\nBLOCK_TRAILER=%s\nKNOWN_INCOMPATIBLE_EDITION=%s\nCOG_WSI_VERSION=%s\n",
		g.Layout, g.BlockOrder, g.BlockLeader, g.BlockTrailer, g.KnownIncompatibleEdition, g.Version,
	)
	if len(body) > 999999 {
		return nil, fmt.Errorf("ghost body too long: %d bytes", len(body))
	}
	header := fmt.Sprintf("GDAL_STRUCTURAL_METADATA_SIZE=%06d bytes\n", len(body))
	return []byte(header + body), nil
}

// ParseGhost parses the ghost area produced by Marshal.
func ParseGhost(b []byte) (Ghost, error) {
	var g Ghost
	s := bufio.NewScanner(bytes.NewReader(b))
	for s.Scan() {
		line := s.Text()
		if strings.HasPrefix(line, "GDAL_STRUCTURAL_METADATA_SIZE=") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "LAYOUT":
			g.Layout = val
		case "BLOCK_ORDER":
			g.BlockOrder = val
		case "BLOCK_LEADER":
			g.BlockLeader = val
		case "BLOCK_TRAILER":
			g.BlockTrailer = val
		case "KNOWN_INCOMPATIBLE_EDITION":
			g.KnownIncompatibleEdition = val
		case "COG_WSI_VERSION":
			g.Version = val
		}
	}
	if err := s.Err(); err != nil {
		return g, err
	}
	if g.Version == "" {
		return g, fmt.Errorf("ghost area missing COG_WSI_VERSION")
	}
	return g, nil
}
