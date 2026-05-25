// Package all blank-imports every shipped quality inspector so they
// all register at init() time. Import for side effects:
//
//	import _ "github.com/wsilabs/wsitools/cmd/wsitools/quality/all"
package all

import (
	_ "github.com/wsilabs/wsitools/cmd/wsitools/quality/jpeg"
	_ "github.com/wsilabs/wsitools/cmd/wsitools/quality/jpeg2000"
	_ "github.com/wsilabs/wsitools/cmd/wsitools/quality/webp"
)
