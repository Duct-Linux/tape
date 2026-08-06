package structs

import (
	"fmt"
	"tape/common/utils"
)

// PkgRef identifies one concrete package build. Every field is interpolated
// into a filesystem path or a URL by the root daemon, so a PkgRef only exists
// in validated form -- construct it with PkgRefFromMap, never by literal.
type PkgRef struct {
	Repo       string
	Name       string
	Version    string
	Subversion string
	Arch       string
}

// PkgRefFromMap validates a client-supplied coordinate map.
//
// The daemon previously passed these five strings straight from the wire into
// filepath.Join("/etc/tape/repos", repo+".toml") and into the download
// destination path. Because path.Join *cleans* "..", a name like
// "../../../../etc/cron.d/x" escaped the temp directory entirely and gave an
// unprivileged caller an arbitrary root-owned file write.
func PkgRefFromMap(m map[string]string) (PkgRef, error) {
	if m == nil {
		return PkgRef{}, fmt.Errorf("package reference is missing")
	}

	ref := PkgRef{
		Repo:       m["repo"],
		Name:       m["name"],
		Version:    m["version"],
		Subversion: m["subversion"],
		Arch:       m["arch"],
	}

	if err := utils.ValidateName(ref.Repo); err != nil {
		return PkgRef{}, fmt.Errorf("invalid repo: %w", err)
	}
	if err := utils.ValidateName(ref.Name); err != nil {
		return PkgRef{}, fmt.Errorf("invalid package name: %w", err)
	}
	if err := utils.ValidateVersion(ref.Version); err != nil {
		return PkgRef{}, fmt.Errorf("invalid version: %w", err)
	}
	if err := utils.ValidateVersion(ref.Subversion); err != nil {
		return PkgRef{}, fmt.Errorf("invalid subversion: %w", err)
	}
	if err := utils.ValidateArch(ref.Arch); err != nil {
		return PkgRef{}, fmt.Errorf("invalid arch: %w", err)
	}

	return ref, nil
}

// ToMap renders the reference in the wire shape the CLI expects.
func (p PkgRef) ToMap() map[string]string {
	return map[string]string{
		"repo":       p.Repo,
		"name":       p.Name,
		"version":    p.Version,
		"subversion": p.Subversion,
		"arch":       p.Arch,
	}
}

func (p PkgRef) String() string {
	return fmt.Sprintf("%s/%s-%s-%s.%s", p.Repo, p.Name, p.Version, p.Subversion, p.Arch)
}
