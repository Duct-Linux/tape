package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"tape/common/arch"
	"tape/common/database"
	"tape/common/logger"
	"tape/common/structs"
	"tape/common/tarUtils"
	commonUtils "tape/common/utils"
	"time"

	"github.com/spf13/viper"
)

// payloadDir is the subdirectory inside a package archive holding the files to
// install. builder wraps the build's install tree under this name.
const payloadDir = "install"

// InstallOptions controls one install transaction.
type InstallOptions struct {
	// Sysroot is the filesystem root to install into. "/" in production; a
	// temporary directory under test.
	Sysroot string

	// Reason records whether the user asked for this package directly or it
	// was pulled in to satisfy a dependency.
	Reason database.InstallReason

	// Repo is the repository the archive came from. Package metadata does not
	// carry it, so the caller supplies it.
	Repo string
}

// InstallPkg installs a downloaded package archive into the sysroot and records
// it in the installed-package database.
//
// The operation is staged: the archive is extracted and hashed, conflicts are
// checked against the database, and only then are files committed to the
// sysroot. If the commit fails partway through, every file written is rolled
// back and every file overwritten is restored, so a failed install does not
// leave the system in a half-updated state.
func InstallPkg(
	archivePath string,
	opts InstallOptions,
	db *database.InstalledDB,
	progress func(int8),
) (*database.InstalledPkg, error) {
	log := logger.NewLogger("daemon", "utils.InstallPkg")

	if opts.Sysroot == "" {
		opts.Sysroot = "/"
	}
	if opts.Reason == "" {
		opts.Reason = database.ReasonExplicit
	}

	sysroot, err := filepath.Abs(opts.Sysroot)
	if err != nil {
		return nil, err
	}

	// --- stage: extract ---------------------------------------------------
	staging, err := os.MkdirTemp("", "tape-install-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)

	archive, err := os.Open(archivePath)
	if err != nil {
		return nil, err
	}
	defer archive.Close()

	// Package payloads legitimately contain symlinks (libfoo.so -> libfoo.so.1),
	// so links are permitted here -- but Untar still confines their targets to
	// the staging directory.
	extractOpts := tarUtils.DefaultUntarOptions
	extractOpts.AllowLinks = true
	if err := tarUtils.UntarWithOptions(staging, archive, extractOpts); err != nil {
		return nil, fmt.Errorf("extracting %s: %w", filepath.Base(archivePath), err)
	}
	reportProgress(progress, 20)

	// --- stage: metadata --------------------------------------------------
	meta, err := readPkgMetadata(staging)
	if err != nil {
		return nil, err
	}
	log.VerboseInfo("installing " + meta.Name + " " + meta.Version + "-" + meta.Subversion)

	// The resolver already filters by architecture, but this is the last point
	// before files land on the system and it is reached by paths the resolver
	// never saw -- a locally built archive, say. Extracting an x86_64 payload
	// onto an aarch64 system produces binaries that cannot run, and if it
	// replaces a system library, one that no longer boots.
	sysArch := arch.Current()
	if !arch.Compatible(meta.Arch, sysArch) {
		return nil, fmt.Errorf("%s is built for %s, this system is %s",
			meta.Name, meta.Arch, sysArch)
	}

	// --- stage: build the manifest ----------------------------------------
	payload := filepath.Join(staging, payloadDir)
	entries, err := collectPayload(payload)
	if err != nil {
		return nil, err
	}
	reportProgress(progress, 40)

	// --- stage: conflict check --------------------------------------------
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		paths = append(paths, e.RelPath)
	}
	conflicts, err := db.CheckConflicts(paths, meta.Name)
	if err != nil {
		return nil, err
	}
	if len(conflicts) > 0 {
		return nil, fmt.Errorf("file conflict: %s (and %d more)", conflicts[0], len(conflicts)-1)
	}
	reportProgress(progress, 50)

	// Files the currently-installed version owns that this one no longer ships.
	// Without this sweep every upgrade leaves unowned files behind that nothing
	// will ever clean up.
	stale, err := staleFiles(db, meta.Name, paths)
	if err != nil {
		return nil, err
	}

	// --- commit: write into the sysroot -----------------------------------
	// Backups live beside their targets rather than in the staging directory,
	// so restoring one is a rename within the same filesystem -- and therefore
	// atomic, like the replacement it undoes.
	tx := &installTx{sysroot: sysroot}

	if err := tx.commit(entries, progress); err != nil {
		if rbErr := tx.rollback(); rbErr != nil {
			// Report both: the rollback failure is the more serious of the two.
			return nil, fmt.Errorf("install failed (%w) and rollback also failed: %v", err, rbErr)
		}
		return nil, err
	}

	// --- commit: record ---------------------------------------------------
	pkg := database.InstalledPkg{
		Name:        meta.Name,
		Version:     meta.Version,
		Subversion:  meta.Subversion,
		Arch:        meta.Arch,
		Repo:        opts.Repo,
		Reason:      opts.Reason,
		InstalledAt: time.Now(),
	}

	files := make([]database.InstalledFile, 0, len(entries))
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		files = append(files, database.InstalledFile{
			Path:   e.RelPath,
			Sha256: e.Sha256,
			Mode:   uint32(e.Mode.Perm()),
		})
	}

	deps := make([]database.InstalledDep, 0, len(meta.Dependencies))
	for name, constraint := range meta.Dependencies {
		deps = append(deps, database.InstalledDep{Name: name, Constraint: constraint})
	}

	if err := db.Record(pkg, files, deps); err != nil {
		// The database is the source of truth; unwind the filesystem so the two
		// do not disagree.
		if rbErr := tx.rollback(); rbErr != nil {
			return nil, fmt.Errorf("recording %s failed (%w) and rollback also failed: %v", meta.Name, err, rbErr)
		}
		return nil, fmt.Errorf("recording %s: %w", meta.Name, err)
	}

	// Only once the new state is durably recorded: if this fails, the install
	// still succeeded and the leftovers are unowned but harmless.
	removeStaleFiles(sysroot, stale, log)

	// The install is committed; the originals are no longer needed. Until this
	// point every one of them could still be renamed back over its target.
	tx.cleanup()

	reportProgress(progress, 100)
	return &pkg, nil
}

// staleFiles returns paths owned by the installed version of name that are
// absent from the incoming manifest.
func staleFiles(db *database.InstalledDB, name string, incoming []string) ([]string, error) {
	previous, err := db.Files(name)
	if err != nil {
		if errors.Is(err, database.ErrNotInstalled) {
			return nil, nil // fresh install: nothing to sweep
		}
		return nil, err
	}

	keep := make(map[string]struct{}, len(incoming))
	for _, p := range incoming {
		keep[p] = struct{}{}
	}

	var stale []string
	for _, f := range previous {
		if _, still := keep[f.Path]; !still {
			stale = append(stale, f.Path)
		}
	}

	return stale, nil
}

// removeStaleFiles deletes superseded files, leaving locally modified ones
// alone for the same reason removal does.
func removeStaleFiles(sysroot string, stale []string, log *logger.Logger) {
	var dirs []string

	for _, rel := range stale {
		target := filepath.Join(sysroot, filepath.FromSlash(rel))
		if !withinRoot(sysroot, target) {
			continue
		}
		if err := os.Remove(target); err != nil {
			if !os.IsNotExist(err) {
				log.VerboseWarning(fmt.Sprintf("could not remove superseded file %s: %s", rel, err))
			}
			continue
		}
		dirs = append(dirs, filepath.Dir(target))
	}

	pruneEmptyDirs(sysroot, dirs)
}

// PkgMetadata is the content of a package's TAPEPACKAGE.toml.
type PkgMetadata struct {
	Name         string
	Version      string
	Subversion   string
	Arch         string
	Repo         string
	Dependencies map[string]string
}

func readPkgMetadata(root string) (*PkgMetadata, error) {
	cfg := viper.New()
	cfg.SetConfigFile(filepath.Join(root, "TAPEPACKAGE.toml"))
	if err := cfg.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("reading TAPEPACKAGE.toml: %w", err)
	}

	meta := &PkgMetadata{
		Name:         cfg.GetString("package.name"),
		Version:      cfg.GetString("package.version"),
		Subversion:   cfg.GetString("package.subversion"),
		Arch:         arch.Normalize(cfg.GetString("package.arch")),
		Dependencies: map[string]string{},
	}

	// The same validation the wire path gets: this metadata becomes a database
	// key and is echoed back to clients.
	if err := commonUtils.ValidateName(meta.Name); err != nil {
		return nil, fmt.Errorf("package metadata: %w", err)
	}
	if err := commonUtils.ValidateVersion(meta.Version); err != nil {
		return nil, fmt.Errorf("package metadata: %w", err)
	}
	if err := commonUtils.ValidateArch(meta.Arch); err != nil {
		return nil, fmt.Errorf("package metadata: %w", err)
	}

	for name, raw := range cfg.GetStringMap("dependencies") {
		// builder writes a nil "build" key rather than deleting it.
		if name == "build" || raw == nil {
			continue
		}
		constraint, ok := raw.(string)
		if !ok {
			continue
		}
		if err := commonUtils.ValidateName(name); err != nil {
			return nil, fmt.Errorf("dependency name %q: %w", name, err)
		}
		meta.Dependencies[name] = constraint
	}

	return meta, nil
}

// payloadEntry is one file staged for installation.
type payloadEntry struct {
	// RelPath is sysroot-relative and slash-separated: the database key.
	RelPath string
	// StagedPath is where the extracted content currently lives.
	StagedPath string
	Mode       fs.FileMode
	Sha256     string
	IsDir      bool
	IsSymlink  bool
	LinkTarget string
}

// collectPayload walks the extracted payload, hashing regular files.
func collectPayload(payload string) ([]payloadEntry, error) {
	info, err := os.Stat(payload)
	if err != nil {
		if os.IsNotExist(err) {
			// A metadata-only package installs nothing. The dev fixtures are
			// exactly this shape.
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("package payload %q is not a directory", payloadDir)
	}

	var entries []payloadEntry

	err = filepath.WalkDir(payload, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == payload {
			return nil
		}

		rel, err := filepath.Rel(payload, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		fi, err := d.Info()
		if err != nil {
			return err
		}

		entry := payloadEntry{RelPath: rel, StagedPath: path, Mode: fi.Mode()}

		// Directories are created but never recorded in the manifest: two
		// packages both shipping /usr/bin must not collide on it. Empty ones
		// still have to be created, since nothing else would bring them into
		// existence -- /var/log/demo is a real package artifact.
		if d.IsDir() {
			entry.IsDir = true
			entries = append(entries, entry)
			return nil
		}

		if fi.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			entry.IsSymlink = true
			entry.LinkTarget = target
			entries = append(entries, entry)
			return nil
		}

		if !fi.Mode().IsRegular() {
			return fmt.Errorf("unsupported file type in payload: %s", rel)
		}

		sum, err := hashFile(path)
		if err != nil {
			return err
		}
		entry.Sha256 = sum
		entries = append(entries, entry)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return entries, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// installTx tracks filesystem changes so they can be undone.
type installTx struct {
	sysroot string

	created []string          // paths that did not exist before
	backups map[string]string // target -> sibling backup holding the original
	dirs    []string          // directories created, shallowest first
}

func (tx *installTx) commit(entries []payloadEntry, progress func(int8)) error {
	tx.backups = map[string]string{}

	for i, entry := range entries {
		target := filepath.Join(tx.sysroot, filepath.FromSlash(entry.RelPath))

		// The payload was already confined by Untar, but this is the point
		// where a path becomes a write as root -- verify it again.
		if !withinRoot(tx.sysroot, target) {
			return fmt.Errorf("refusing to write %q outside sysroot %q", target, tx.sysroot)
		}

		if err := tx.ensureParent(filepath.Dir(target)); err != nil {
			return err
		}
		// Directories are shared and never replaced, so there is nothing to
		// preserve for them.
		if !entry.IsDir {
			if err := tx.stash(target); err != nil {
				return err
			}
		}

		if err := tx.place(entry, target); err != nil {
			return err
		}

		if progress != nil && len(entries) > 0 {
			// Files occupy the 50-100% band; extraction and hashing took the rest.
			reportProgress(progress, int8(50+(50*(i+1))/len(entries)))
		}
	}

	return nil
}

// place installs one entry by writing a sibling temporary file and renaming it
// over the target.
//
// This is what makes replacing a live shared library safe. Writing in place
// (open with O_TRUNC) pulls the pages out from under every process that has the
// file mmap'd, and unlinking before creating leaves an interval where the path
// does not resolve at all. rename(2) is atomic: the path refers to either the
// old file or the new one at every instant, never to nothing and never to a
// half-written file. Processes still holding the old inode keep running against
// it until they exit.
func (tx *installTx) place(entry payloadEntry, target string) error {
	dir := filepath.Dir(target)

	if entry.IsDir {
		// Already present is fine -- directories are shared, not owned.
		if _, err := os.Stat(target); err == nil {
			return nil
		}
		if err := os.MkdirAll(target, entry.Mode.Perm()); err != nil {
			return err
		}
		tx.dirs = append(tx.dirs, target)
		return nil
	}

	if entry.IsSymlink {
		// os.Symlink cannot overwrite, so build it under a temporary name and
		// rename it into place.
		tmpLink := filepath.Join(dir, tempName("lnk"))
		if err := os.Symlink(entry.LinkTarget, tmpLink); err != nil {
			return err
		}
		if err := os.Rename(tmpLink, target); err != nil {
			os.Remove(tmpLink)
			return err
		}
		return nil
	}

	// Same directory as the target, so the rename stays within one filesystem
	// and is therefore atomic.
	tmp, err := os.CreateTemp(dir, ".tape-new-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	committed := false
	defer func() {
		if !committed {
			tmp.Close()
			os.Remove(tmpPath)
		}
	}()

	src, err := os.Open(entry.StagedPath)
	if err != nil {
		return err
	}
	defer src.Close()

	if _, err := io.Copy(tmp, src); err != nil {
		return err
	}
	// Durable before it becomes visible: a crash must not leave a file that
	// exists but has no contents.
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Chmod(entry.Mode.Perm()); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, target); err != nil {
		return err
	}
	committed = true

	return nil
}

// ensureParent creates a directory, remembering any level it had to create so
// rollback can prune them.
func (tx *installTx) ensureParent(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		return nil
	}

	// Record every level that does not yet exist, shallowest first.
	var missing []string
	for d := dir; withinRoot(tx.sysroot, d) && d != tx.sysroot; d = filepath.Dir(d) {
		if _, err := os.Stat(d); err == nil {
			break
		}
		missing = append([]string{d}, missing...)
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tx.dirs = append(tx.dirs, missing...)
	return nil
}

// stash preserves whatever currently occupies target, without disturbing the
// path itself.
//
// A hardlink gives a second name for the same inode at zero cost and leaves the
// original name in place -- unlike renaming it away, which is what opened the
// window where the file was missing.
func (tx *installTx) stash(target string) error {
	info, err := os.Lstat(target)
	if err != nil {
		if os.IsNotExist(err) {
			tx.created = append(tx.created, target)
			return nil
		}
		return err
	}

	dir := filepath.Dir(target)
	backupPath := filepath.Join(dir, tempName("bak"))

	if info.Mode()&os.ModeSymlink != 0 {
		// Hardlinking a symlink is not portable; recreate it instead. The
		// original link is untouched either way.
		linkTarget, err := os.Readlink(target)
		if err != nil {
			return err
		}
		if err := os.Symlink(linkTarget, backupPath); err != nil {
			return err
		}
		tx.backups[target] = backupPath
		return nil
	}

	if err := os.Link(target, backupPath); err != nil {
		// Filesystems without hardlink support (or a target that is not a
		// regular file) fall back to a copy.
		if copyErr := copyFileContents(target, backupPath, info.Mode().Perm()); copyErr != nil {
			return fmt.Errorf("backing up %s: %w", target, copyErr)
		}
	}

	tx.backups[target] = backupPath
	return nil
}

// rollback undoes everything commit did.
func (tx *installTx) rollback() error {
	var firstErr error

	for _, path := range tx.created {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
	}

	// Renaming the backup back over the target restores it atomically, the same
	// way it was replaced.
	for original, backup := range tx.backups {
		if err := os.Rename(backup, original); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	// Deepest first, and only if empty.
	for i := len(tx.dirs) - 1; i >= 0; i-- {
		os.Remove(tx.dirs[i])
	}

	tx.created = nil
	tx.backups = map[string]string{}
	tx.dirs = nil

	return firstErr
}

// cleanup discards the backups once the install is durably recorded.
func (tx *installTx) cleanup() {
	for _, backup := range tx.backups {
		os.Remove(backup)
	}
	tx.backups = map[string]string{}
}

// tempName produces a hidden, unlikely-to-collide sibling filename. The random
// suffix comes from crypto/rand via os.CreateTemp semantics where possible; for
// symlinks and backups a counter plus the process id is sufficient, since the
// name only has to be unique within this transaction.
func tempName(kind string) string {
	tempCounter++
	return fmt.Sprintf(".tape-%s-%d-%d", kind, os.Getpid(), tempCounter)
}

var tempCounter int

// copyFileContents is the fallback used when a backup cannot be hardlinked.
func copyFileContents(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func withinRoot(root, p string) bool {
	if p == root {
		return true
	}
	return strings.HasPrefix(p, strings.TrimSuffix(root, string(os.PathSeparator))+string(os.PathSeparator))
}

func reportProgress(progress func(int8), value int8) {
	if progress == nil {
		return
	}
	if value > 100 {
		value = 100
	}
	progress(value)
}

// InstallOptionsFromConfig reads the sysroot from configuration.
func InstallOptionsFromConfig(cfg *viper.Viper, reason database.InstallReason) InstallOptions {
	return InstallOptions{
		Sysroot: cfg.GetString("daemon.sysroot"),
		Reason:  reason,
	}
}

// ResolveRefFromMetadata rebuilds a package reference from installed metadata.
func ResolveRefFromMetadata(meta *PkgMetadata) structs.PkgRef {
	return structs.PkgRef{
		Repo:       meta.Repo,
		Name:       meta.Name,
		Version:    meta.Version,
		Subversion: meta.Subversion,
		Arch:       meta.Arch,
	}
}
