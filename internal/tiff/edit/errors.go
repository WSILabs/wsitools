package edit

import "errors"

var (
	ErrBadMagic         = errors.New("tiff/edit: bad TIFF magic")
	ErrUnexpectedLayout = errors.New("tiff/edit: unexpected TIFF byte layout")
	ErrOverlap          = errors.New("tiff/edit: overlapping byte ranges in file")
	ErrUnknownType      = errors.New("tiff/edit: unknown tag type")
)
