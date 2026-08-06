//go:build unix

package tarUtils

import "syscall"

// oNoFollow makes OpenFile fail rather than follow a symlink at the final path
// component, so a pre-planted link cannot redirect an extraction write.
const oNoFollow = syscall.O_NOFOLLOW
