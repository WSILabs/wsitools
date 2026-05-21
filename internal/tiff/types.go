package tiff

// TIFF data type constants (TIFF 6.0 §2, BigTIFF additions).
const (
	TypeBYTE     uint16 = 1
	TypeASCII    uint16 = 2
	TypeSHORT    uint16 = 3
	TypeLONG     uint16 = 4
	TypeRATIONAL uint16 = 5
	// TypeUNDEFINED is TIFF type 7. Used for opaque byte arrays like
	// JPEGTables (tag 347) and ICCProfile (tag 34675). Spec says these
	// tags MUST use type UNDEFINED rather than BYTE; many readers accept
	// either, but wsiwriter v0.6.0 used UNDEFINED, so streamwriter must
	// too to remain bit-exact.
	TypeUNDEFINED uint16 = 7
	TypeDOUBLE    uint16 = 12
	TypeLONG8     uint16 = 16
	TypeIFD8      uint16 = 18
)

// TypeByteSize returns the byte length of one value of the given TIFF
// type, or 0 if the type is unknown.
func TypeByteSize(t uint16) int {
	switch t {
	case TypeBYTE, TypeASCII, TypeUNDEFINED:
		return 1
	case TypeSHORT:
		return 2
	case TypeLONG:
		return 4
	case TypeRATIONAL, TypeDOUBLE, TypeLONG8, TypeIFD8:
		return 8
	}
	return 0
}
