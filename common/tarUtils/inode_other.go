//go:build !unix

package tarUtils

import "io/fs"

// Hard links are not detected on platforms without stat's device and inode
// fields. Every name is then stored as its own file, which is what happened
// everywhere before this existed: correct, merely larger.
func inodeOf(fs.FileInfo) (inodeKey, bool) { return inodeKey{}, false }
