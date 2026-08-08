package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"tape/common/database"
	"tape/common/logger"
)

// RemoveOptions controls one removal.
type RemoveOptions struct {
	// Sysroot is the filesystem root to remove from.
	Sysroot string

	// Force removes the package even when other installed packages still
	// depend on it.
	Force bool
}

// RemoveResult describes what a removal did.
type RemoveResult struct {
	Name         string
	FilesRemoved int
	// Orphans lists dependency-installed packages that nothing needs any more.
	Orphans []string
}

// RemovePkg deletes an installed package's files and its database record.
//
// Files are removed using the recorded manifest, so only paths this package
// actually owns are touched. A file whose content no longer matches the
// recorded hash is left in place: it has been modified since installation and
// is more likely to be local configuration than something safe to delete.
func RemovePkg(
	name string,
	opts RemoveOptions,
	db *database.InstalledDB,
) (*RemoveResult, error) {
	log := logger.NewLogger("daemon", "utils.RemovePkg")

	if opts.Sysroot == "" {
		opts.Sysroot = "/"
	}
	sysroot, err := filepath.Abs(opts.Sysroot)
	if err != nil {
		return nil, err
	}

	if _, err := db.Get(name); err != nil {
		return nil, err
	}

	// Refuse to break other packages unless explicitly told to.
	dependents, err := db.RequiredBy(name)
	if err != nil {
		return nil, err
	}
	if len(dependents) > 0 && !opts.Force {
		return nil, fmt.Errorf("%s is required by %v; use --force to remove it anyway", name, dependents)
	}

	// Deepest paths first, so directories are empty by the time they are pruned.
	files, err := db.Files(name)
	if err != nil {
		return nil, err
	}

	removed := 0
	var prunable []string

	for _, file := range files {
		target := filepath.Join(sysroot, filepath.FromSlash(file.Path))
		if !withinRoot(sysroot, target) {
			// A manifest row should never point outside the sysroot; if one
			// does, the database has been tampered with.
			log.Warning(fmt.Sprintf("skipping %s: outside sysroot", file.Path))
			continue
		}

		info, err := os.Lstat(target)
		if err != nil {
			if os.IsNotExist(err) {
				continue // already gone
			}
			return nil, err
		}

		// Leave locally modified files behind rather than destroying edits.
		if info.Mode().IsRegular() && file.Sha256 != "" {
			sum, err := hashFile(target)
			if err != nil {
				return nil, err
			}
			if sum != file.Sha256 {
				log.Warning(fmt.Sprintf("keeping modified file %s", file.Path))
				continue
			}
		}

		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		removed++
		prunable = append(prunable, filepath.Dir(target))
	}

	pruneEmptyDirs(sysroot, prunable)

	if err := db.Remove(name); err != nil {
		return nil, err
	}

	orphans, err := db.Orphans()
	if err != nil {
		return nil, err
	}

	// Removing a library leaves the cache pointing at a file that is gone,
	// which fails at load time exactly like a missing one.
	removedPaths := make([]string, 0, len(files))
	for _, f := range files {
		removedPaths = append(removedPaths, f.Path)
	}
	RefreshLinkerCache(sysroot, removedPaths, log)

	return &RemoveResult{Name: name, FilesRemoved: removed, Orphans: orphans}, nil
}

// pruneEmptyDirs removes now-empty directories, walking upward but never past
// the sysroot. os.Remove fails on a non-empty directory, which is exactly the
// guard needed: a directory shared with another package stays.
func pruneEmptyDirs(sysroot string, dirs []string) {
	seen := map[string]struct{}{}

	for _, dir := range dirs {
		for d := dir; withinRoot(sysroot, d) && d != sysroot; d = filepath.Dir(d) {
			if _, done := seen[d]; done {
				break
			}
			seen[d] = struct{}{}
			if err := os.Remove(d); err != nil {
				break // not empty, or not ours: stop climbing
			}
		}
	}
}
