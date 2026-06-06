package main

import (
	"errors"
	"fmt"

	"github.com/wsilabs/wsitools/internal/source"
	"github.com/wsilabs/wsitools/internal/tiff/edit"
)

var (
	// ErrNoSuchAssociated means the slide has no associated image of the
	// requested type.
	ErrNoSuchAssociated = errors.New("associated image not present")
	// ErrUnsupportedAssoc means associated editing is not supported for the
	// source format (no IFD offset available).
	ErrUnsupportedAssoc = errors.New("associated editing not supported for this format")
)

// locateAssociated finds the chain-order IFD index of the associated image of
// the given type within file. It returns ErrNoSuchAssociated if the slide has
// no such image, or ErrUnsupportedAssoc if the format cannot map it to an IFD.
func locateAssociated(src source.Source, file *edit.File, typ string) (int, source.AssociatedImage, error) {
	var target source.AssociatedImage
	for _, a := range src.Associated() {
		if a.Type() == typ {
			target = a
			break
		}
	}
	if target == nil {
		return -1, nil, fmt.Errorf("%w: %s", ErrNoSuchAssociated, typ)
	}
	off, ok := target.IFDOffset()
	if !ok {
		return -1, nil, fmt.Errorf("%w: %s", ErrUnsupportedAssoc, typ)
	}
	for i := range file.IFDs {
		if file.IFDs[i].Offset == uint64(off) {
			return i, target, nil
		}
	}
	return -1, nil, fmt.Errorf("%w: IFD at offset %d not found in chain", edit.ErrUnexpectedLayout, off)
}
