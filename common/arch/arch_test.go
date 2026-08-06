package arch

import "testing"

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		// x86
		"x86_64": X86_64, "amd64": X86_64, "X86_64": X86_64, "x86-64": X86_64,
		"i686": I686, "i386": I686, "x86": I686,

		// 64-bit ARM -- the spellings uname, Go and distributions each use.
		"aarch64": Aarch64, "arm64": Aarch64, "armv8": Aarch64, "armv8l": Aarch64,

		// 32-bit ARM. armv7 and armv6 are distinct: armv7 code traps on armv6.
		"armv7h": Armv7h, "armv7": Armv7h, "armv7l": Armv7h, "armhf": Armv7h,
		"armv6h": Armv6h, "armv6": Armv6h, "armv6l": Armv6h,

		"riscv64": Riscv64,
		"any":     Any, "noarch": Any, "all": Any,

		// Target triples, as passed to builder --target.
		"aarch64-linux-gnu":    Aarch64,
		"x86_64-linux-gnu":     X86_64,
		"armv7l-linux-gnueabi": Armv7h,

		// Whitespace and case.
		"  aarch64  ": Aarch64,
		"AARCH64":     Aarch64,

		// Unknown architectures pass through rather than being rejected.
		"loong64": "loong64",
		"":        "",
	}

	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := Normalize(in); got != want {
				t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
			}
		})
	}
}

func TestFromGoArch(t *testing.T) {
	cases := map[string]string{
		"amd64":   X86_64,
		"arm64":   Aarch64,
		"386":     I686,
		"riscv64": Riscv64,
		// GOARCH cannot distinguish armv6 from armv7; armv7h is the default,
		// overridable with TAPE_ARCH.
		"arm": Armv7h,
	}

	for in, want := range cases {
		if got := FromGoArch(in); got != want {
			t.Errorf("FromGoArch(%q) = %q, want %q", in, got, want)
		}
	}
}

// The bug this package exists to prevent: an x86_64 package installing on ARM.
func TestCompatibleRefusesForeignArchitectures(t *testing.T) {
	foreign := []struct{ pkg, sys string }{
		{X86_64, Aarch64},
		{Aarch64, X86_64},
		{I686, Aarch64},
		{Armv7h, Aarch64},
		{Aarch64, Armv7h},
		// armv7 binaries use instructions armv6 hardware does not have.
		{Armv7h, Armv6h},
		// And the reverse is refused too: running below the system baseline is
		// not what the distribution intends to ship.
		{Armv6h, Armv7h},
	}

	for _, c := range foreign {
		t.Run(c.pkg+"_on_"+c.sys, func(t *testing.T) {
			if Compatible(c.pkg, c.sys) {
				t.Errorf("Compatible(%q, %q) = true, want false", c.pkg, c.sys)
			}
		})
	}
}

func TestCompatibleAcceptsMatchingAndAny(t *testing.T) {
	matching := []struct{ pkg, sys string }{
		{X86_64, X86_64},
		{Aarch64, Aarch64},
		{Armv7h, Armv7h},
		{Armv6h, Armv6h},
		{Riscv64, Riscv64},

		// "any" runs everywhere.
		{Any, Aarch64},
		{Any, X86_64},
		{"noarch", Armv7h},

		// Different spellings of the same architecture must match.
		{"arm64", Aarch64},
		{"armv8l", "aarch64"},
		{"amd64", X86_64},
		{"armhf", Armv7h},
	}

	for _, c := range matching {
		t.Run(c.pkg+"_on_"+c.sys, func(t *testing.T) {
			if !Compatible(c.pkg, c.sys) {
				t.Errorf("Compatible(%q, %q) = false, want true", c.pkg, c.sys)
			}
		})
	}
}

// A system architecture of "any" is meaningless and must not act as a wildcard
// that accepts every package.
func TestCompatibleDoesNotTreatAnySystemAsWildcard(t *testing.T) {
	if Compatible(X86_64, Any) {
		t.Error("a system arch of \"any\" accepted an x86_64 package")
	}
}

func TestCurrentHonoursOverride(t *testing.T) {
	t.Setenv("TAPE_ARCH", "armv6l")
	if got := Current(); got != Armv6h {
		t.Errorf("Current() with TAPE_ARCH=armv6l = %q, want %q", got, Armv6h)
	}

	// The override is normalised like anything else.
	t.Setenv("TAPE_ARCH", "aarch64-linux-gnu")
	if got := Current(); got != Aarch64 {
		t.Errorf("Current() = %q, want %q", got, Aarch64)
	}
}

func TestCurrentFallsBackToBuildArch(t *testing.T) {
	t.Setenv("TAPE_ARCH", "")
	got := Current()
	if got == "" {
		t.Fatal("Current() returned an empty architecture")
	}
	if !IsKnown(got) {
		t.Logf("Current() = %q, which is not a known architecture (fine if this host is exotic)", got)
	}
}

func TestIsKnown(t *testing.T) {
	for _, name := range []string{"x86_64", "aarch64", "armv7h", "armv6h", "i686", "riscv64", "any", "arm64", "armhf"} {
		if !IsKnown(name) {
			t.Errorf("IsKnown(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "sparc", "totally-made-up"} {
		if IsKnown(name) {
			t.Errorf("IsKnown(%q) = true, want false", name)
		}
	}
}

// The link-time default must be overridable at runtime, and must itself
// override the GOARCH mapping -- that ordering is what lets an armv6 build
// identify correctly while still being testable.
func TestCurrentPrecedence(t *testing.T) {
	original := buildArch
	t.Cleanup(func() { buildArch = original })

	buildArch = "armv6l"
	t.Setenv("TAPE_ARCH", "")
	if got := Current(); got != Armv6h {
		t.Errorf("Current() with baked armv6l = %q, want %q", got, Armv6h)
	}

	t.Setenv("TAPE_ARCH", "aarch64")
	if got := Current(); got != Aarch64 {
		t.Errorf("TAPE_ARCH did not override the baked architecture: got %q", got)
	}
}
