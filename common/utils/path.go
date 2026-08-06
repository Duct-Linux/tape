package utils

import "path/filepath"

// ResolvePath interprets a user-supplied path relative to pwd, leaving absolute
// paths alone.
//
// The tools previously used path.Join(pwd, arg) unconditionally, which silently
// rewrote an absolute argument into a path *under* the working directory:
//
//	tape-builder build ./pkg -o /tmp/out
//
// wrote to ./tmp/out instead of /tmp/out, and reported success either way.
func ResolvePath(pwd, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(pwd, p)
}
