package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff"
)

type rawIFDJSON struct {
	Index       int            `json:"index"`
	Offset      int64          `json:"offset"`
	IsBigTIFF   bool           `json:"is_bigtiff"`
	IsSubIFD    bool           `json:"is_subifd"`
	ParentIndex int            `json:"parent_index"`
	ByteOrder   string         `json:"byte_order"`
	Entries     []rawEntryJSON `json:"entries"`
}

type rawEntryJSON struct {
	Tag         uint16 `json:"tag"`
	Name        string `json:"name"`
	Type        uint16 `json:"type"`
	TypeName    string `json:"type_name"`
	Count       uint64 `json:"count"`
	Value       any    `json:"value"`
	Interpreted string `json:"interpreted,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
	Error       string `json:"error,omitempty"`
}

func renderRawJSON(w io.Writer, ifds []source.IFDRecord, fullValues bool) error {
	out := make([]rawIFDJSON, 0, len(ifds))
	for _, ifd := range ifds {
		obj := rawIFDJSON{
			Index:       ifd.Index,
			Offset:      ifd.Offset,
			IsBigTIFF:   ifd.IsBigTIFF,
			IsSubIFD:    ifd.IsSubIFD,
			ParentIndex: -1,
			ByteOrder:   "little",
		}
		if ifd.IsSubIFD {
			obj.ParentIndex = ifd.ParentIndex
		}
		if ifd.ByteOrder == binary.BigEndian {
			obj.ByteOrder = "big"
		}
		for _, e := range ifd.Entries {
			obj.Entries = append(obj.Entries, encodeRawEntryJSON(e, ifd.ByteOrder, fullValues))
		}
		out = append(out, obj)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func encodeRawEntryJSON(e source.RawEntry, bo binary.ByteOrder, fullValues bool) rawEntryJSON {
	je := rawEntryJSON{
		Tag:      e.Tag,
		Name:     tiff.TagName(e.Tag),
		Type:     e.Type,
		TypeName: tiff.TypeName(e.Type),
		Count:    e.Count,
	}
	if e.Raw == nil {
		je.Error = "unreadable"
		return je
	}
	v, err := decodeValue(e.Type, e.Count, e.Raw, bo)
	if err != nil {
		je.Error = err.Error()
		return je
	}
	var truncated bool
	v, truncated = truncateValue(v, fullValues)
	je.Truncated = truncated

	if e.Type == 1 || e.Type == 7 {
		je.Value = bytesToHex(v.([]byte))
	} else {
		je.Value = v
	}

	if s := interpretScalarJSON(e.Tag, v); s != "" {
		je.Interpreted = s
	}
	return je
}

func interpretScalarJSON(tag uint16, v any) string {
	var n uint64
	switch x := v.(type) {
	case uint64:
		n = x
	case int64:
		if x < 0 {
			return ""
		}
		n = uint64(x)
	default:
		return ""
	}
	return tiff.InterpretEnum(tag, n)
}

// renderRawText prints a tiffinfo-style dump of every IFD.
func renderRawText(w io.Writer, ifds []source.IFDRecord, fullValues bool) error {
	for i, ifd := range ifds {
		writeIFDHeader(w, ifd)
		var nameColW, typeColW int
		for _, e := range ifd.Entries {
			name := tiff.TagName(e.Tag)
			if l := len(tagLabel(e.Tag, name)); l > nameColW {
				nameColW = l
			}
			if tn := tiff.TypeName(e.Type); len(tn) > typeColW {
				typeColW = len(tn)
			}
		}
		for _, e := range ifd.Entries {
			renderRawEntryText(w, e, ifd.ByteOrder, nameColW, typeColW, fullValues)
		}
		if i < len(ifds)-1 {
			fmt.Fprintln(w)
		}
	}
	return nil
}

func writeIFDHeader(w io.Writer, ifd source.IFDRecord) {
	kind := "top-level"
	if ifd.IsSubIFD {
		kind = fmt.Sprintf("subIFD of IFD %d", ifd.ParentIndex)
	}
	bigOrClassic := "classic TIFF"
	if ifd.IsBigTIFF {
		bigOrClassic = "BigTIFF"
	}
	bo := "little-endian"
	if ifd.ByteOrder == binary.BigEndian {
		bo = "big-endian"
	}
	fmt.Fprintf(w, "IFD %d @ offset 0x%x (%s, %s, byte-order=%s)\n",
		ifd.Index, ifd.Offset, kind, bigOrClassic, bo)
}

func tagLabel(tag uint16, name string) string {
	if name == "" {
		return fmt.Sprintf("%d (unknown)", tag)
	}
	return fmt.Sprintf("%d (%s)", tag, name)
}

func renderRawEntryText(w io.Writer, e source.RawEntry, bo binary.ByteOrder, nameColW, typeColW int, fullValues bool) {
	name := tiff.TagName(e.Tag)
	label := tagLabel(e.Tag, name)
	typeName := tiff.TypeName(e.Type)
	valStr := formatValueText(e, bo, fullValues)
	fmt.Fprintf(w, "  %-*s  %-*s  count=%-6d value=%s\n",
		nameColW, label, typeColW, typeName, e.Count, valStr)
}

func formatValueText(e source.RawEntry, bo binary.ByteOrder, fullValues bool) string {
	if e.Raw == nil {
		return "<unreadable>"
	}
	v, err := decodeValue(e.Type, e.Count, e.Raw, bo)
	if err != nil {
		return "<unreadable>"
	}
	var truncated bool
	v, truncated = truncateValue(v, fullValues)

	if e.Type == 1 || e.Type == 7 {
		b := v.([]byte)
		hex := bytesToHex(b)
		more := ""
		if truncated {
			more = " ..."
		}
		return fmt.Sprintf("<%d bytes: %s%s>", e.Count, hex, more)
	}

	if s := interpretScalar(e.Tag, v); s != "" {
		return s
	}

	if e.Type == 2 {
		s := v.(string)
		suffix := ""
		if truncated {
			suffix = "…"
		}
		return fmt.Sprintf("%q%s", s, suffix)
	}

	switch x := v.(type) {
	case []uint64:
		s := joinUints(x)
		if truncated {
			s += fmt.Sprintf(" (... %d more)", int(e.Count)-len(x))
		}
		return "[" + s + "]"
	case []int64:
		s := joinInts(x)
		if truncated {
			s += fmt.Sprintf(" (... %d more)", int(e.Count)-len(x))
		}
		return "[" + s + "]"
	case []float64:
		s := joinFloats(x)
		if truncated {
			s += fmt.Sprintf(" (... %d more)", int(e.Count)-len(x))
		}
		return "[" + s + "]"
	case []string:
		s := strings.Join(x, ", ")
		if truncated {
			s += fmt.Sprintf(" (... %d more)", int(e.Count)-len(x))
		}
		return "[" + s + "]"
	}

	return fmt.Sprintf("%v", v)
}

// interpretScalar returns "<name> (<num>)" for enum-recognised scalar
// integer values, or "" otherwise.
func interpretScalar(tag uint16, v any) string {
	var n uint64
	switch x := v.(type) {
	case uint64:
		n = x
	case int64:
		if x < 0 {
			return ""
		}
		n = uint64(x)
	default:
		return ""
	}
	name := tiff.InterpretEnum(tag, n)
	if name == "" {
		return ""
	}
	return fmt.Sprintf("%s (%d)", name, n)
}

func bytesToHex(b []byte) string {
	const hexChars = "0123456789abcdef"
	out := make([]byte, 0, len(b)*3)
	for i, c := range b {
		if i > 0 {
			out = append(out, ' ')
		}
		out = append(out, hexChars[c>>4], hexChars[c&0x0f])
	}
	return string(out)
}

func joinUints(xs []uint64) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = fmt.Sprintf("%d", x)
	}
	return strings.Join(parts, ", ")
}

func joinInts(xs []int64) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = fmt.Sprintf("%d", x)
	}
	return strings.Join(parts, ", ")
}

func joinFloats(xs []float64) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = fmt.Sprintf("%g", x)
	}
	return strings.Join(parts, ", ")
}

// decodeValue turns a raw TIFF entry byte slice into a native Go value.
// For count==1 returns a scalar; for count>1 returns a slice (except
// ASCII, which always returns a string; and BYTE/UNDEFINED/SBYTE/unknown,
// which always return []byte/[]int8).
func decodeValue(typ uint16, count uint64, raw []byte, bo binary.ByteOrder) (any, error) {
	if raw == nil {
		return nil, fmt.Errorf("unreadable")
	}
	switch typ {
	case 1, 7:
		return append([]byte(nil), raw...), nil
	case 2:
		s := string(raw)
		for i := 0; i < len(s); i++ {
			if s[i] == 0 {
				s = s[:i]
				break
			}
		}
		return s, nil
	case 3:
		return decodeUintArray(raw, count, 2, bo)
	case 4, 13:
		return decodeUintArray(raw, count, 4, bo)
	case 5:
		return decodeRationalArray(raw, count, bo, false)
	case 6:
		out := make([]int8, len(raw))
		for i, b := range raw {
			out[i] = int8(b)
		}
		return out, nil
	case 8:
		return decodeIntArray(raw, count, 2, bo)
	case 9:
		return decodeIntArray(raw, count, 4, bo)
	case 10:
		return decodeRationalArray(raw, count, bo, true)
	case 11:
		if count == 1 && len(raw) >= 4 {
			return float64(math.Float32frombits(bo.Uint32(raw[:4]))), nil
		}
		out := make([]float64, count)
		for i := uint64(0); i < count; i++ {
			out[i] = float64(math.Float32frombits(bo.Uint32(raw[i*4 : i*4+4])))
		}
		return out, nil
	case 12:
		if count == 1 && len(raw) >= 8 {
			return math.Float64frombits(bo.Uint64(raw[:8])), nil
		}
		out := make([]float64, count)
		for i := uint64(0); i < count; i++ {
			out[i] = math.Float64frombits(bo.Uint64(raw[i*8 : i*8+8]))
		}
		return out, nil
	case 16, 18:
		return decodeUintArray(raw, count, 8, bo)
	case 17:
		return decodeIntArray(raw, count, 8, bo)
	default:
		return append([]byte(nil), raw...), nil
	}
}

func decodeUintArray(raw []byte, count uint64, elem int, bo binary.ByteOrder) (any, error) {
	readOne := func(b []byte) uint64 {
		switch elem {
		case 2:
			return uint64(bo.Uint16(b))
		case 4:
			return uint64(bo.Uint32(b))
		case 8:
			return bo.Uint64(b)
		}
		return 0
	}
	if count == 1 && len(raw) >= elem {
		return readOne(raw[:elem]), nil
	}
	out := make([]uint64, count)
	for i := uint64(0); i < count; i++ {
		out[i] = readOne(raw[int(i)*elem : int(i)*elem+elem])
	}
	return out, nil
}

func decodeIntArray(raw []byte, count uint64, elem int, bo binary.ByteOrder) (any, error) {
	readOne := func(b []byte) int64 {
		switch elem {
		case 2:
			return int64(int16(bo.Uint16(b)))
		case 4:
			return int64(int32(bo.Uint32(b)))
		case 8:
			return int64(bo.Uint64(b))
		}
		return 0
	}
	if count == 1 && len(raw) >= elem {
		return readOne(raw[:elem]), nil
	}
	out := make([]int64, count)
	for i := uint64(0); i < count; i++ {
		out[i] = readOne(raw[int(i)*elem : int(i)*elem+elem])
	}
	return out, nil
}

// Truncation caps used by truncateValue when fullValues==false.
const (
	maxArrayEntries = 8
	maxAsciiChars   = 200
	maxBlobBytes    = 64
)

// truncateValue applies smart-truncation rules to a decoded value. When
// fullValues is true, returns the value unchanged with truncated=false.
func truncateValue(v any, fullValues bool) (any, bool) {
	if fullValues {
		return v, false
	}
	switch x := v.(type) {
	case []uint64:
		if len(x) > maxArrayEntries {
			return x[:maxArrayEntries], true
		}
	case []int64:
		if len(x) > maxArrayEntries {
			return x[:maxArrayEntries], true
		}
	case []float64:
		if len(x) > maxArrayEntries {
			return x[:maxArrayEntries], true
		}
	case []string:
		if len(x) > maxArrayEntries {
			return x[:maxArrayEntries], true
		}
	case []byte:
		if len(x) > maxBlobBytes {
			return x[:maxBlobBytes], true
		}
	case []int8:
		if len(x) > maxBlobBytes {
			return x[:maxBlobBytes], true
		}
	case string:
		if len(x) > maxAsciiChars {
			return x[:maxAsciiChars], true
		}
	}
	return v, false
}

func decodeRationalArray(raw []byte, count uint64, bo binary.ByteOrder, signed bool) (any, error) {
	render := func(off int) string {
		if signed {
			n := int32(bo.Uint32(raw[off : off+4]))
			d := int32(bo.Uint32(raw[off+4 : off+8]))
			return fmt.Sprintf("%d/%d", n, d)
		}
		n := bo.Uint32(raw[off : off+4])
		d := bo.Uint32(raw[off+4 : off+8])
		return fmt.Sprintf("%d/%d", n, d)
	}
	if count == 1 && len(raw) >= 8 {
		return render(0), nil
	}
	out := make([]string, count)
	for i := uint64(0); i < count; i++ {
		out[i] = render(int(i) * 8)
	}
	return out, nil
}
