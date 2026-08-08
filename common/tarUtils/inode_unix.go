//go:build unix

package tarUtils

import (
	"io/fs"
	"syscall"
)

// inodeOf returns the device and inode of a file, and whether the file has
// more than one link. Files with a single link cannot be hard links to
// anything, so they are not worth tracking.
func inodeOf(fi fs.FileInfo) (inodeKey, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st.Nlink < 2 {
		return inodeKey{}, false
	}
	return inodeKey{dev: uint64(st.Dev), ino: uint64(st.Ino)}, true
}
