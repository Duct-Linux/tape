package utils

import (
	"fmt"
	"strings"
)

// Names and versions arriving from a client are interpolated into filesystem
// paths and URLs by a root-privileged daemon. Validation is allowlist-based:
// anything not explicitly permitted is refused, so a new metacharacter cannot
// quietly become exploitable later.

const maxNameLen = 128

// ValidateName checks an identifier used as a single path component -- a
// package name or a repository key.
//
// It must be a non-empty, reasonably short string of [A-Za-z0-9._+-] that does
// not begin with punctuation. That rules out "..", absolute paths, nested
// paths, and anything a shell or argument parser would treat specially.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("name must not be empty")
	}
	if len(name) > maxNameLen {
		return fmt.Errorf("name %q exceeds %d characters", truncate(name), maxNameLen)
	}

	for _, r := range name {
		if !isNameRune(r) {
			return fmt.Errorf("name %q contains disallowed character %q", truncate(name), r)
		}
	}

	// A leading '-' is read as a flag by anything that shells out; a leading
	// '.' hides the entry and opens the "." / ".." confusion.
	switch name[0] {
	case '-', '.', '+', '_':
		return fmt.Errorf("name %q must not start with %q", truncate(name), name[0])
	}

	return nil
}

// ValidateVersion checks a version or subversion string. It is deliberately
// broader than ValidateName -- epochs (1:2.3), build metadata (+build.5) and
// tildes (1.0~beta) are all legitimate -- but still excludes separators and
// shell metacharacters.
func ValidateVersion(version string) error {
	if version == "" {
		return fmt.Errorf("version must not be empty")
	}
	if len(version) > maxNameLen {
		return fmt.Errorf("version %q exceeds %d characters", truncate(version), maxNameLen)
	}

	for _, r := range version {
		if !isNameRune(r) && r != ':' && r != '~' {
			return fmt.Errorf("version %q contains disallowed character %q", truncate(version), r)
		}
	}
	if strings.Contains(version, "..") {
		return fmt.Errorf("version %q contains %q", truncate(version), "..")
	}
	if version[0] == '.' || version[0] == '-' {
		return fmt.Errorf("version %q must not start with %q", truncate(version), version[0])
	}

	return nil
}

// ValidateArch checks an architecture tag such as "x86_64" or "aarch64".
func ValidateArch(arch string) error {
	if arch == "" {
		return fmt.Errorf("arch must not be empty")
	}
	if len(arch) > 32 {
		return fmt.Errorf("arch %q is too long", truncate(arch))
	}

	for _, r := range arch {
		isAlnum := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !isAlnum && r != '_' {
			return fmt.Errorf("arch %q contains disallowed character %q", truncate(arch), r)
		}
	}

	return nil
}

func isNameRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z':
		return true
	case r >= 'A' && r <= 'Z':
		return true
	case r >= '0' && r <= '9':
		return true
	case r == '.' || r == '_' || r == '+' || r == '-':
		return true
	default:
		return false
	}
}

// truncate keeps hostile input from flooding logs and error messages.
func truncate(s string) string {
	const limit = 64
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}
