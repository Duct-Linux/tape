package tarUtils

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// UntarOptions bounds what an archive is allowed to do to the filesystem.
//
// The zero value is not useful; callers should start from DefaultUntarOptions.
type UntarOptions struct {
	// MaxEntries caps the number of archive members processed.
	MaxEntries int64

	// MaxTotalBytes caps the total uncompressed payload written. This is the
	// defence against decompression bombs -- a few KB of gzip can otherwise
	// expand to fill the disk.
	MaxTotalBytes int64

	// AllowLinks permits symlink and hardlink members. Even when enabled, link
	// targets are still required to resolve inside the destination.
	AllowLinks bool

	// PreserveSetuid keeps setuid/setgid/sticky bits from the archive. Off by
	// default: an archive that can set those bits on an extracted file can hand
	// out root.
	PreserveSetuid bool
}

// DefaultUntarOptions is the policy used by Untar: no links, no setuid, and
// generous but finite size limits.
var DefaultUntarOptions = UntarOptions{
	MaxEntries:     100_000,
	MaxTotalBytes:  4 << 30, // 4 GiB
	AllowLinks:     false,
	PreserveSetuid: false,
}

// Untar extracts a gzipped tar stream into dst using DefaultUntarOptions.
//
// Every member is confined to dst: absolute paths, ".." traversal, symlinks
// pointing outside, and writes through a symlinked parent directory are all
// rejected. Extraction stops at the first violation.
func Untar(dst string, r io.Reader) error {
	return UntarWithOptions(dst, r, DefaultUntarOptions)
}

// UntarWithOptions extracts a gzipped tar stream into dst under an explicit policy.
func UntarWithOptions(dst string, r io.Reader, opts UntarOptions) error {
	// Resolve the destination up front. Everything is compared against the
	// resolved form, so a symlinked dst (/tmp -> /private/tmp on darwin) does
	// not produce spurious containment failures.
	base, err := resolveBase(dst)
	if err != nil {
		return err
	}

	gzr, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	var (
		entries int64
		written int64
	)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if header == nil {
			continue
		}

		entries++
		if opts.MaxEntries > 0 && entries > opts.MaxEntries {
			return fmt.Errorf("tar: archive exceeds entry limit of %d", opts.MaxEntries)
		}

		target, err := secureJoin(base, header.Name)
		if err != nil {
			return err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, sanitizeMode(header.Mode, 0755, opts)); err != nil {
				return err
			}
			if err := verifyContained(base, target); err != nil {
				return err
			}

		case tar.TypeReg:
			if opts.MaxTotalBytes > 0 && written+header.Size > opts.MaxTotalBytes {
				return fmt.Errorf("tar: archive exceeds size limit of %d bytes", opts.MaxTotalBytes)
			}

			n, err := writeRegular(base, target, tr, header, opts)
			written += n
			if err != nil {
				return err
			}
			if opts.MaxTotalBytes > 0 && written > opts.MaxTotalBytes {
				return fmt.Errorf("tar: archive exceeds size limit of %d bytes", opts.MaxTotalBytes)
			}

		case tar.TypeSymlink:
			if !opts.AllowLinks {
				return fmt.Errorf("tar: symlink entry %q rejected by policy", header.Name)
			}
			if err := validateLinkTarget(base, target, header.Linkname); err != nil {
				return err
			}
			if err := prepareParent(base, target); err != nil {
				return err
			}
			// Replace rather than fail on an existing node, but never follow it.
			if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
				return err
			}
			if err := os.Symlink(header.Linkname, target); err != nil {
				return err
			}

		case tar.TypeLink:
			if !opts.AllowLinks {
				return fmt.Errorf("tar: hardlink entry %q rejected by policy", header.Name)
			}
			linkTarget, err := secureJoin(base, header.Linkname)
			if err != nil {
				return fmt.Errorf("tar: hardlink %q: %w", header.Name, err)
			}
			if err := prepareParent(base, target); err != nil {
				return err
			}
			if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
				return err
			}
			if err := os.Link(linkTarget, target); err != nil {
				return err
			}

		default:
			// Device nodes, FIFOs, sockets and anything exotic. A package
			// payload has no legitimate use for these, and they were silently
			// dropped before -- which hid malformed archives.
			return fmt.Errorf("tar: unsupported entry type %q for %q", header.Typeflag, header.Name)
		}
	}
}

// resolveBase returns the symlink-resolved absolute form of dst, creating it if
// it does not yet exist.
func resolveBase(dst string) (string, error) {
	if err := os.MkdirAll(dst, 0755); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(dst)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// secureJoin maps an archive member name to an absolute path inside base,
// rejecting anything that tries to leave.
func secureJoin(base, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("tar: empty entry name")
	}

	// Archive names are always slash-separated, regardless of host OS.
	if path.IsAbs(name) || filepath.IsAbs(name) {
		return "", fmt.Errorf("tar: absolute entry name %q rejected", name)
	}
	// Reject drive-letter and UNC style names outright.
	//
	// Only a drive *prefix* is rejected, not every colon. A colon is an
	// ordinary character in a POSIX filename and legitimate packages contain
	// them: perl ships manual pages called App::Cpan.3, and refusing the whole
	// archive over one made perl unpackageable.
	if hasVolumePrefix(name) {
		return "", fmt.Errorf("tar: entry name %q looks like a volume path", name)
	}

	slashed := strings.ReplaceAll(name, `\`, "/")

	// Reject ".." on the *raw* name, before cleaning. path.Clean("/"+name)
	// silently absorbs leading ".." (nothing is above root), which would turn
	// "../escaped" into a contained -- but relocated -- write. A package whose
	// payload carries ".." is malformed or hostile either way; refuse it rather
	// than quietly rewriting where its files land.
	for _, element := range strings.Split(slashed, "/") {
		if element == ".." {
			return "", fmt.Errorf("tar: entry name %q escapes the destination", name)
		}
	}

	cleaned := strings.TrimPrefix(path.Clean("/"+slashed), "/")
	if cleaned == "" || cleaned == "." {
		return "", fmt.Errorf("tar: entry name %q resolves to the destination itself", name)
	}

	target := filepath.Join(base, filepath.FromSlash(cleaned))
	if err := containedIn(base, target); err != nil {
		return "", fmt.Errorf("tar: entry name %q escapes the destination", name)
	}
	return target, nil
}

// containedIn reports whether p is base itself or lies beneath it, lexically.
func containedIn(base, p string) error {
	if p == base {
		return nil
	}
	if strings.HasPrefix(p, base+string(os.PathSeparator)) {
		return nil
	}
	return fmt.Errorf("tar: %q is outside %q", p, base)
}

// verifyContained re-checks an existing path after symlink resolution. The
// lexical check in secureJoin cannot see a symlink planted by an earlier entry
// (or already present on disk), so paths are re-verified once they exist.
func verifyContained(base, p string) error {
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return containedIn(base, resolved)
}

// prepareParent creates the parent directory of target and confirms that the
// parent -- after following any symlinks -- is still inside base. This is what
// stops the "symlink a directory outward, then write through it" attack, which
// O_NOFOLLOW on the final component alone does not catch.
func prepareParent(base, target string) error {
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return err
	}
	return containedIn(base, resolved)
}

func writeRegular(base, target string, tr io.Reader, header *tar.Header, opts UntarOptions) (int64, error) {
	if err := prepareParent(base, target); err != nil {
		return 0, err
	}

	mode := sanitizeMode(header.Mode, 0644, opts)

	// O_TRUNC so a short file does not inherit a longer file's tail bytes.
	// O_NOFOLLOW so a pre-planted symlink at the final component cannot
	// redirect the write.
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|oNoFollow, mode)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	// Bound the copy even if the header lies about Size.
	limit := header.Size
	if opts.MaxTotalBytes > 0 && limit > opts.MaxTotalBytes {
		limit = opts.MaxTotalBytes
	}
	n, err := io.Copy(f, io.LimitReader(tr, limit))
	if err != nil {
		return n, err
	}

	// OpenFile's permission argument is masked by umask; set the intended mode
	// explicitly so extraction is reproducible.
	if err := f.Chmod(mode); err != nil {
		return n, err
	}

	return n, nil
}

// validateLinkTarget ensures a symlink cannot point outside base, whether it is
// absolute or climbs out with "..".
func validateLinkTarget(base, linkPath, linkname string) error {
	if linkname == "" {
		return fmt.Errorf("tar: empty symlink target")
	}
	if path.IsAbs(linkname) || filepath.IsAbs(linkname) {
		return fmt.Errorf("tar: absolute symlink target %q rejected", linkname)
	}

	// Resolve the link relative to its own directory, as the kernel would.
	resolved := filepath.Join(filepath.Dir(linkPath), filepath.FromSlash(linkname))
	if err := containedIn(base, resolved); err != nil {
		return fmt.Errorf("tar: symlink target %q escapes the destination", linkname)
	}
	return nil
}

// sanitizeMode converts an archive mode to a permission set, dropping
// setuid/setgid/sticky unless explicitly preserved.
func sanitizeMode(mode int64, fallback os.FileMode, opts UntarOptions) os.FileMode {
	perm := os.FileMode(mode).Perm()
	if perm == 0 {
		perm = fallback
	}
	if opts.PreserveSetuid {
		// tar stores POSIX mode bits (04000/02000/01000); os.FileMode spells
		// the same concepts with entirely different bit positions, so they have
		// to be translated rather than masked across.
		if mode&syscallSetuid != 0 {
			perm |= os.ModeSetuid
		}
		if mode&syscallSetgid != 0 {
			perm |= os.ModeSetgid
		}
		if mode&syscallSticky != 0 {
			perm |= os.ModeSticky
		}
	}
	return perm
}

// POSIX mode bits as they appear in a tar header.
const (
	syscallSetuid = 0o4000
	syscallSetgid = 0o2000
	syscallSticky = 0o1000
)

// hasVolumePrefix reports whether an archive name begins with a Windows drive
// specifier such as "C:".
//
// The danger is an archive extracted on Windows, where "C:\evil" is an absolute
// path that escapes the destination. On Unix the same string is merely an oddly
// named file, but the check is unconditional so a package cannot be harmless on
// one host and hostile on another.
//
// Only the *first* component is examined, because only the first component can
// make a path absolute: "sub/C:/x" is relative on Windows too. Checking every
// component instead -- as this function first did -- rejects perl, whose manual
// pages are named after modules like B::Concise and Pod::Text. A leading "B:"
// and a drive letter are indistinguishable in isolation; in position they are
// not.
func hasVolumePrefix(name string) bool {
	first := name
	if i := strings.IndexAny(name, `/\`); i >= 0 {
		first = name[:i]
	}
	if len(first) < 2 || first[1] != ':' {
		return false
	}
	c := first[0]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
