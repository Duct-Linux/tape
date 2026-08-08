package utils

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"tape/common/logger"
)

// RefreshLinkerCache regenerates /etc/ld.so.cache when a transaction has
// changed the set of shared libraries on the system.
//
// Without this, a freshly installed library is invisible to the dynamic linker
// until something else happens to rebuild the cache: the file is on disk, the
// package is recorded, and every binary that needs it fails with "cannot open
// shared object file". Every distribution runs ldconfig for exactly this
// reason, normally from a post-install hook. tape has no hooks, so until now
// the cache was only ever built at image assembly time -- fine for an image,
// wrong for a running system, which is where installs actually happen.
//
// Failure is logged and never fatal. The install itself has already committed
// and been recorded; a stale cache is a degradation to repair, not a reason to
// unwind a good install. It also legitimately fails when assembling a sysroot
// for a foreign architecture, where the target's ldconfig cannot run at all.
func RefreshLinkerCache(sysroot string, paths []string, log *logger.Logger) {
	if !touchesSharedLibrary(paths) {
		return
	}

	if sysroot == "" {
		sysroot = "/"
	}

	bin := findLdconfig(sysroot)
	if bin == "" {
		log.VerboseInfo("no ldconfig in " + sysroot + "; leaving the linker cache alone")
		return
	}

	// The sysroot's own ldconfig is used rather than the host's: it is built
	// against the same glibc as the libraries it is indexing, and on a foreign
	// sysroot the host's would write a cache in the wrong format. -r makes it
	// treat the sysroot as / for both reading and writing.
	//
	// This executes a binary out of the sysroot. That is safe here because the
	// sysroot is set by daemon config, which only root can write -- it is not
	// something a client can name in a request. Were that ever to change, this
	// would become a way to have the root daemon run a chosen binary.
	var cmd *exec.Cmd
	if sysroot == "/" {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(bin, "-r", sysroot)
	}

	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			msg = ": " + msg
		}
		log.VerboseInfo("ldconfig failed, linker cache may be stale" + msg)
		return
	}
	log.VerboseInfo("linker cache refreshed")
}

// touchesSharedLibrary reports whether any path looks like a shared object.
//
// Matching on the name rather than reading ELF headers: ldconfig is cheap and
// idempotent, so a false positive costs a few milliseconds, while a false
// negative leaves a library the linker cannot find.
func touchesSharedLibrary(paths []string) bool {
	for _, p := range paths {
		base := filepath.Base(p)
		if base == "" {
			continue
		}
		// libfoo.so, libfoo.so.1, libfoo.so.1.2.3 -- but not libfoo.something.
		if i := strings.Index(base, ".so"); i > 0 {
			rest := base[i+len(".so"):]
			if rest == "" || strings.HasPrefix(rest, ".") {
				return true
			}
		}
	}
	return false
}

// findLdconfig locates ldconfig inside a sysroot. glibc installs it in
// /usr/sbin; /sbin is the same file on a merged-/usr system and a different one
// elsewhere, so both are tried.
func findLdconfig(sysroot string) string {
	for _, rel := range []string{"usr/sbin/ldconfig", "sbin/ldconfig", "usr/bin/ldconfig"} {
		candidate := filepath.Join(sysroot, rel)
		fi, err := os.Stat(candidate)
		if err != nil || fi.IsDir() || fi.Mode().Perm()&0o111 == 0 {
			continue
		}
		return candidate
	}
	return ""
}
