// Package quality defines the Inspector interface and registry used
// by wsitools' info command to surface per-level codec quality
// parameters (JPEG Q estimate, JPEG 2000 layer count, WebP
// lossless flag, etc.).
//
// Each codec gets an Inspector in a subpackage (quality/jpeg,
// quality/jpeg2000, quality/webp). Subpackages register themselves
// via Register in init(). The cmd/wsitools/quality/all subpackage
// blank-imports all shipped inspectors; the info command imports
// that for "all available codecs registered."
//
// Adding a new inspector requires no changes to the info command —
// just a new subpackage that calls quality.Register.
package quality
