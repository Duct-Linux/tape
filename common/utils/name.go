package utils

import "strings"

// FoldName returns the key a package name is matched by.
//
// Package names are compared case-insensitively everywhere tape matches one,
// because the two halves of the system disagree about case and, for everything
// already published, always will: tape-builder read [dependencies] through
// viper, which lower-cases every key it returns, so every manifest published
// before that was fixed says "libxau" where the package it names is "libXau".
// The builder fix stops new manifests being written that way; this tolerance is
// what keeps the old ones installable, with no rebuild and no republish. The
// two are a pair -- do not make matching case-sensitive again on the grounds
// that the builder now gets it right, because the index still holds years of
// manifests that do not.
//
// The canonical spelling is always the one the index row carries. Folding
// decides what MATCHES; it never decides what is recorded or reported, so
// nothing downstream sees a name the publisher did not write.
//
// ValidateName restricts names to [A-Za-z0-9._+-], so this is ASCII folding and
// agrees exactly with SQLite's NOCASE collation, which is also ASCII-only. The
// SQL lookups and this function must fold identically, or a name would match in
// one place and not the other.
func FoldName(name string) string { return strings.ToLower(name) }
