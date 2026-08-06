// Package arch models the machine architectures tape builds and installs for.
//
// Architecture was previously a free-form string that nothing checked: the
// resolver matched packages by name alone, so an aarch64 machine would happily
// resolve, download and install an x86_64 package.
package arch

import (
	"os"
	"runtime"
	"strings"
)

// Any marks a package that runs anywhere: documentation, fonts, scripts.
const Any = "any"

// Canonical architecture names. These are what appears in package filenames
// and in the repository index, so they are part of the on-disk format.
const (
	X86_64  = "x86_64"
	I686    = "i686"
	Aarch64 = "aarch64"
	Armv7h  = "armv7h"
	Armv6h  = "armv6h"
	Riscv64 = "riscv64"
)

// goarchNames maps Go's GOARCH values to tape names.
//
// GOARCH is "arm" for every 32-bit ARM variant, but armv6 and armv7 are not
// interchangeable -- an armv7 binary fails on armv6 hardware. Go does not
// expose GOARM at runtime, so 32-bit ARM defaults to armv7h (what current ARM
// distributions target) and must be overridden on armv6 hardware. See Current.
var goarchNames = map[string]string{
	"amd64":   X86_64,
	"386":     I686,
	"arm64":   Aarch64,
	"arm":     Armv7h,
	"riscv64": Riscv64,
}

// aliases maps the spellings found in the wild to canonical names.
var aliases = map[string]string{
	"x86-64":  X86_64,
	"x8664":   X86_64,
	"amd64":   X86_64,
	"i386":    I686,
	"i486":    I686,
	"i586":    I686,
	"i686":    I686,
	"x86":     I686,
	"arm64":   Aarch64,
	"armv8":   Aarch64,
	"armv8l":  Aarch64,
	"aarch64": Aarch64,
	"armv7":   Armv7h,
	"armv7l":  Armv7h,
	"armv7h":  Armv7h,
	"armhf":   Armv7h,
	"armv6":   Armv6h,
	"armv6l":  Armv6h,
	"armv6h":  Armv6h,
	"arm":     Armv7h,
	"riscv64": Riscv64,
	"rv64":    Riscv64,
	"any":     Any,
	"noarch":  Any,
	"all":     Any,
}

// Normalize maps an architecture spelling to its canonical tape name.
//
// It accepts a bare architecture ("aarch64") or a target triple
// ("aarch64-linux-gnu"), since builder's --target takes the latter.
// Unrecognised values are returned lowercased and otherwise unchanged, so a new
// architecture works without a code change.
func Normalize(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}

	// Try the whole string first. Splitting on "-" unconditionally would turn
	// "x86-64" into "x86", i.e. silently downgrade a 64-bit name to a 32-bit
	// architecture.
	if canonical, ok := aliases[name]; ok {
		return canonical
	}

	// Otherwise treat it as a target triple, whose first component is the
	// architecture ("aarch64-linux-gnu").
	if before, _, found := strings.Cut(name, "-"); found {
		if canonical, ok := aliases[before]; ok {
			return canonical
		}
		return before
	}

	return name
}

// buildArch is baked in at link time:
//
//	go build -ldflags "-X tape/common/arch.buildArch=armv6h"
//
// This is how a distribution pins the architecture of the binaries it ships.
// It matters most on 32-bit ARM, where GOARCH is just "arm" for both armv6 and
// armv7 and Go does not expose GOARM at runtime -- so without it, an armv6
// build would identify itself as armv7h and accept packages its hardware
// cannot execute.
var buildArch string

// Current returns the architecture this system installs packages for.
//
// Resolution order:
//
//	TAPE_ARCH             runtime override, for testing and odd systems
//	buildArch             baked in at link time by the distribution
//	runtime.GOARCH        what this binary was built for
func Current() string {
	if override := os.Getenv("TAPE_ARCH"); override != "" {
		return Normalize(override)
	}
	if buildArch != "" {
		return Normalize(buildArch)
	}
	return FromGoArch(runtime.GOARCH)
}

// FromGoArch maps a GOARCH value to a tape architecture name.
func FromGoArch(goarch string) string {
	if name, ok := goarchNames[goarch]; ok {
		return name
	}
	return strings.ToLower(goarch)
}

// Compatible reports whether a package built for pkgArch can be installed on a
// system running sysArch.
//
// The rule is deliberately strict: an exact match, or a package marked "any".
// ARM in particular tempts a looser rule -- armv6 code does run on armv7
// hardware -- but "runs" is not the same as "is what the distribution intends
// to ship", and silently installing a lower baseline than the system supports
// is how mixed-ABI systems end up unexplainably slow or subtly broken.
func Compatible(pkgArch, sysArch string) bool {
	pkg := Normalize(pkgArch)
	sys := Normalize(sysArch)

	if pkg == Any {
		return true
	}
	return pkg == sys
}

// IsKnown reports whether a name is one tape recognises. Used to warn about a
// likely typo without refusing an architecture tape has not heard of.
func IsKnown(name string) bool {
	switch Normalize(name) {
	case X86_64, I686, Aarch64, Armv7h, Armv6h, Riscv64, Any:
		return true
	default:
		return false
	}
}
