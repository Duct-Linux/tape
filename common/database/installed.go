package database

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// InstallReason records why a package is on the system. Packages pulled in only
// to satisfy a dependency become removable once nothing needs them.
type InstallReason string

const (
	ReasonExplicit   InstallReason = "explicit"
	ReasonDependency InstallReason = "dependency"
)

// InstalledPkg is one package currently installed on the system.
//
// These models deliberately do not embed gorm.Model: its DeletedAt field turns
// every delete into a soft delete, which would silently break the uniqueness of
// Name and make "is this installed?" ambiguous.
type InstalledPkg struct {
	ID          uint   `gorm:"primaryKey"`
	Name        string `gorm:"uniqueIndex;not null"`
	Version     string `gorm:"not null"`
	Subversion  string
	Arch        string
	Repo        string
	Reason      InstallReason `gorm:"not null"`
	InstalledAt time.Time
}

func (InstalledPkg) TableName() string { return "installed_packages" }

// InstalledFile is one path owned by an installed package, recorded relative to
// the sysroot with forward slashes.
//
// The unique index on Path is what makes file-conflict detection possible: two
// packages cannot claim the same path.
type InstalledFile struct {
	ID     uint   `gorm:"primaryKey"`
	PkgID  uint   `gorm:"index;not null"`
	Path   string `gorm:"uniqueIndex;not null"`
	Sha256 string
	Mode   uint32
}

func (InstalledFile) TableName() string { return "installed_files" }

// InstalledDep is a dependency edge, kept so orphans and reverse dependencies
// can be computed without re-reading every package's metadata.
type InstalledDep struct {
	ID         uint   `gorm:"primaryKey"`
	PkgID      uint   `gorm:"index;not null"`
	Name       string `gorm:"index;not null"`
	Constraint string
}

func (InstalledDep) TableName() string { return "installed_dependencies" }

// ErrNotInstalled is returned when a package is not present on the system.
var ErrNotInstalled = errors.New("package is not installed")

// FileConflict reports a path claimed by more than one package.
type FileConflict struct {
	Path  string
	Owner string
}

func (c FileConflict) Error() string {
	return fmt.Sprintf("%s is already owned by %s", c.Path, c.Owner)
}

// InstalledDB is the local record of what is installed.
type InstalledDB struct {
	db *gorm.DB
}

// OpenInstalledDB opens (creating if needed) the installed-package database.
func OpenInstalledDB(dbPath string) (*InstalledDB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, err
	}

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		// gorm logs every statement at Info by default, which would flood
		// /var/log/tape.log.
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, err
	}

	if err := db.AutoMigrate(&InstalledPkg{}, &InstalledFile{}, &InstalledDep{}); err != nil {
		return nil, err
	}

	return &InstalledDB{db: db}, nil
}

// Close releases the underlying connection pool. The repo databases are opened
// per query and never closed, which leaks a pool per lookup for the lifetime of
// the daemon; this one is explicit.
func (i *InstalledDB) Close() error {
	sqlDB, err := i.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// Get returns the installed package with the given name.
func (i *InstalledDB) Get(name string) (*InstalledPkg, error) {
	var pkg InstalledPkg
	tx := i.db.Where("name = ?", name).First(&pkg)
	if tx.Error != nil {
		if errors.Is(tx.Error, gorm.ErrRecordNotFound) {
			return nil, ErrNotInstalled
		}
		return nil, tx.Error
	}
	return &pkg, nil
}

// IsInstalled reports whether a package is present.
func (i *InstalledDB) IsInstalled(name string) (bool, error) {
	_, err := i.Get(name)
	if errors.Is(err, ErrNotInstalled) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// List returns every installed package, ordered by name.
func (i *InstalledDB) List() ([]InstalledPkg, error) {
	var pkgs []InstalledPkg
	tx := i.db.Order("name asc").Find(&pkgs)
	return pkgs, tx.Error
}

// Files returns the manifest for an installed package, deepest path first so
// callers can unlink and then prune parent directories in order.
func (i *InstalledDB) Files(name string) ([]InstalledFile, error) {
	pkg, err := i.Get(name)
	if err != nil {
		return nil, err
	}

	var files []InstalledFile
	tx := i.db.Where("pkg_id = ?", pkg.ID).Order("length(path) desc").Find(&files)
	return files, tx.Error
}

// FileOwner returns the package owning a sysroot-relative path, or
// ErrNotInstalled if the path is unowned.
func (i *InstalledDB) FileOwner(path string) (string, error) {
	var file InstalledFile
	tx := i.db.Where("path = ?", path).First(&file)
	if tx.Error != nil {
		if errors.Is(tx.Error, gorm.ErrRecordNotFound) {
			return "", ErrNotInstalled
		}
		return "", tx.Error
	}

	var pkg InstalledPkg
	if tx := i.db.Where("id = ?", file.PkgID).First(&pkg); tx.Error != nil {
		return "", tx.Error
	}
	return pkg.Name, nil
}

// CheckConflicts reports paths already owned by a different package. Paths
// owned by excludePkg itself are ignored, so reinstalling or upgrading a
// package does not conflict with its own files.
func (i *InstalledDB) CheckConflicts(paths []string, excludePkg string) ([]FileConflict, error) {
	if len(paths) == 0 {
		return nil, nil
	}

	var conflicts []FileConflict

	// Chunked to stay well under SQLite's variable limit on large packages.
	const chunk = 500
	for start := 0; start < len(paths); start += chunk {
		end := start + chunk
		if end > len(paths) {
			end = len(paths)
		}

		var files []InstalledFile
		if tx := i.db.Where("path IN ?", paths[start:end]).Find(&files); tx.Error != nil {
			return nil, tx.Error
		}

		for _, f := range files {
			var owner InstalledPkg
			if tx := i.db.Where("id = ?", f.PkgID).First(&owner); tx.Error != nil {
				return nil, tx.Error
			}
			if owner.Name == excludePkg {
				continue
			}
			conflicts = append(conflicts, FileConflict{Path: f.Path, Owner: owner.Name})
		}
	}

	return conflicts, nil
}

// Record writes a package, its manifest and its dependency edges atomically,
// replacing any previous record of the same package.
func (i *InstalledDB) Record(pkg InstalledPkg, files []InstalledFile, deps []InstalledDep) error {
	return i.db.Transaction(func(tx *gorm.DB) error {
		// Replace wholesale so an upgrade cannot leave stale manifest rows.
		var existing InstalledPkg
		found := tx.Where("name = ?", pkg.Name).First(&existing)
		if found.Error == nil {
			if err := deletePkgRows(tx, existing.ID); err != nil {
				return err
			}
			if err := tx.Delete(&InstalledPkg{}, existing.ID).Error; err != nil {
				return err
			}
		} else if !errors.Is(found.Error, gorm.ErrRecordNotFound) {
			return found.Error
		}

		if pkg.InstalledAt.IsZero() {
			pkg.InstalledAt = time.Now()
		}
		pkg.ID = 0
		if err := tx.Create(&pkg).Error; err != nil {
			return err
		}

		for idx := range files {
			files[idx].ID = 0
			files[idx].PkgID = pkg.ID
		}
		if len(files) > 0 {
			if err := tx.CreateInBatches(files, 200).Error; err != nil {
				return err
			}
		}

		for idx := range deps {
			deps[idx].ID = 0
			deps[idx].PkgID = pkg.ID
		}
		if len(deps) > 0 {
			if err := tx.CreateInBatches(deps, 200).Error; err != nil {
				return err
			}
		}

		return nil
	})
}

// Remove deletes a package's record, manifest and dependency edges. It does not
// touch the filesystem; callers unlink the files returned by Files first.
func (i *InstalledDB) Remove(name string) error {
	return i.db.Transaction(func(tx *gorm.DB) error {
		var pkg InstalledPkg
		if err := tx.Where("name = ?", name).First(&pkg).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotInstalled
			}
			return err
		}
		if err := deletePkgRows(tx, pkg.ID); err != nil {
			return err
		}
		return tx.Delete(&InstalledPkg{}, pkg.ID).Error
	})
}

// RequiredBy returns the installed packages that depend on name.
func (i *InstalledDB) RequiredBy(name string) ([]string, error) {
	var deps []InstalledDep
	if tx := i.db.Where("name = ?", name).Find(&deps); tx.Error != nil {
		return nil, tx.Error
	}

	var dependents []string
	for _, d := range deps {
		var pkg InstalledPkg
		if tx := i.db.Where("id = ?", d.PkgID).First(&pkg); tx.Error != nil {
			if errors.Is(tx.Error, gorm.ErrRecordNotFound) {
				continue
			}
			return nil, tx.Error
		}
		dependents = append(dependents, pkg.Name)
	}

	return dependents, nil
}

// Orphans returns dependency-installed packages that nothing depends on any
// more -- the candidates for cleanup after a removal.
func (i *InstalledDB) Orphans() ([]string, error) {
	var pkgs []InstalledPkg
	if tx := i.db.Where("reason = ?", ReasonDependency).Order("name asc").Find(&pkgs); tx.Error != nil {
		return nil, tx.Error
	}

	var orphans []string
	for _, pkg := range pkgs {
		dependents, err := i.RequiredBy(pkg.Name)
		if err != nil {
			return nil, err
		}
		if len(dependents) == 0 {
			orphans = append(orphans, pkg.Name)
		}
	}

	return orphans, nil
}

func deletePkgRows(tx *gorm.DB, pkgID uint) error {
	if err := tx.Where("pkg_id = ?", pkgID).Delete(&InstalledFile{}).Error; err != nil {
		return err
	}
	return tx.Where("pkg_id = ?", pkgID).Delete(&InstalledDep{}).Error
}
