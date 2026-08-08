package buildsteps

import (
	"debug/elf"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"tape/common/database"
	"tape/common/global"
	"tape/common/logger"
)

// checkDeclaredDeps compares the shared libraries a package's binaries actually
// link against with the dependencies its recipe declares.
//
// Nothing checked this before, so a package could install cleanly and then fail
// to run. The rust package declared glibc, gcc and zlib but not openssl, and
// `tape install rust` produced a working rustc beside a cargo that could not
// start at all: "libssl.so.4: cannot open shared object file". The build had
// succeeded because the build image happened to contain openssl -- which is the
// general shape of this bug, not a detail of that package.
//
// Reported, never fatal. The build environment is not always a Duct system with
// an installed database to resolve sonames against -- a from-scratch bootstrap
// in duct/chroot is not -- and a check that cannot see ownership must not
// invent a failure. What it can always do is name what is missing.
func checkDeclaredDeps(payload string, declared map[string]any, log *logger.Logger) {
	needed, provided := scanELF(payload)
	if len(needed) == 0 {
		return
	}

	// Sonames the package ships itself are satisfied internally.
	for soname := range provided {
		delete(needed, soname)
	}

	db, err := database.OpenInstalledDB(installedDBPath())

	if err != nil {
		// No installed database: the sonames are still worth listing, because a
		// name absent from the build environment entirely is a real problem
		// regardless of who would have owned it.
		var unresolved []string
		for soname := range needed {
			if findLibrary(soname) == "" {
				unresolved = append(unresolved, soname)
			}
		}
		if len(unresolved) > 0 {
			sort.Strings(unresolved)
			log.Warning("links against libraries not present in the build environment: " +
				strings.Join(unresolved, ", "))
		}
		return
	}
	defer db.Close()

	declaredSet := make(map[string]struct{}, len(declared))
	for name := range declared {
		if name != "build" {
			declaredSet[name] = struct{}{}
		}
	}

	missing := make(map[string][]string)
	for soname := range needed {
		path := findLibrary(soname)
		if path == "" {
			missing["(not found)"] = append(missing["(not found)"], soname)
			continue
		}
		// FileOwner takes a sysroot-relative path: the database records
		// "usr/lib/libssl.so.4", not "/usr/lib/libssl.so.4".
		owner, err := db.FileOwner(strings.TrimPrefix(path, "/"))
		if err != nil || owner == "" {
			continue // not owned by any package: part of the image, not a dependency
		}
		if _, ok := declaredSet[owner]; !ok {
			missing[owner] = append(missing[owner], soname)
		}
	}

	if len(missing) == 0 {
		return
	}
	owners := make([]string, 0, len(missing))
	for owner := range missing {
		owners = append(owners, owner)
	}
	sort.Strings(owners)
	for _, owner := range owners {
		sort.Strings(missing[owner])
		log.Warning("undeclared dependency on " + owner +
			" (linked: " + strings.Join(missing[owner], ", ") +
			"); add it to [dependencies] or the package will install and not run")
	}
}

// installedDBPath is where the daemon records what is installed. The default
// matches config's, so this works in an image assembled by tape even when the
// builder is run with no config file of its own.
func installedDBPath() string {
	if cfg := global.GetConfig(); cfg != nil {
		if p := cfg.GetString("daemon.installed-db"); p != "" {
			return p
		}
	}
	return "/var/lib/tape/installed.db"
}

// scanELF walks a payload and returns the sonames its binaries need, and the
// sonames it provides itself.
func scanELF(payload string) (needed, provided map[string]struct{}) {
	needed = make(map[string]struct{})
	provided = make(map[string]struct{})

	_ = filepath.WalkDir(payload, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		f, err := elf.Open(path)
		if err != nil {
			return nil // not an ELF, which most files are not
		}
		defer f.Close()

		if libs, err := f.DynString(elf.DT_NEEDED); err == nil {
			for _, l := range libs {
				needed[l] = struct{}{}
			}
		}
		if sonames, err := f.DynString(elf.DT_SONAME); err == nil {
			for _, s := range sonames {
				provided[s] = struct{}{}
			}
		}
		// A library may be linked by file name rather than by soname.
		provided[filepath.Base(path)] = struct{}{}
		return nil
	})
	return needed, provided
}

// findLibrary locates a soname in the usual places. Deliberately not a full
// ld.so search: this reports, and a miss becomes "(not found)" rather than a
// wrong answer.
func findLibrary(soname string) string {
	for _, dir := range []string{"/usr/lib", "/usr/lib64", "/lib", "/lib64", "/usr/local/lib"} {
		candidate := filepath.Join(dir, soname)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}
