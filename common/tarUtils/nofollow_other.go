//go:build !unix

package tarUtils

// Platforms without O_NOFOLLOW fall back to 0. Containment still holds via the
// symlink-resolving parent check in prepareParent; only the final-component
// race window is wider. tape targets Linux, so this path is a build convenience.
const oNoFollow = 0
