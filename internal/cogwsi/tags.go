package cogwsi

import "github.com/cornish/wsitools/internal/wsiwriter"

// WSI tag IDs aliased from internal/wsiwriter (range 65080–65084).
const (
	TagWSIImageType    = wsiwriter.TagWSIImageType
	TagWSILevelIndex   = wsiwriter.TagWSILevelIndex
	TagWSILevelCount   = wsiwriter.TagWSILevelCount
	TagWSISourceFormat = wsiwriter.TagWSISourceFormat
	TagWSIToolsVersion = wsiwriter.TagWSIToolsVersion
)

// New COG-WSI v0.1 private tags (range 65085–65087). All DOUBLE (TIFF type 12).
const (
	TagWSIMPPX          uint16 = 65085
	TagWSIMPPY          uint16 = 65086
	TagWSIMagnification uint16 = 65087
)

// WSIImageType canonical values used by COG-WSI v0.1 (subset of wsiwriter's).
const (
	WSIImageTypePyramid   = wsiwriter.WSIImageTypePyramid
	WSIImageTypeLabel     = wsiwriter.WSIImageTypeLabel
	WSIImageTypeMacro     = wsiwriter.WSIImageTypeMacro
	WSIImageTypeOverview  = wsiwriter.WSIImageTypeOverview
	WSIImageTypeThumbnail = wsiwriter.WSIImageTypeThumbnail
)
