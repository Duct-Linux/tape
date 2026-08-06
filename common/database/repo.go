package database

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"tape/common/config"
	"tape/common/logger"
	"tape/common/utils"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type RepoModelPkgs struct {
	gorm.Model
	Name       string
	Version    string
	Subversion string
	Arch       string

	// Sha256 and Size cover the package archive this row describes. They are
	// what extends the repository signature to the packages themselves: the
	// index is signed, the index carries these digests, so a verified index
	// means every package it lists can be verified too -- without signing each
	// archive individually.
	Sha256 string
	Size   int64
}

func (RepoModelPkgs) TableName() string {
	return "packages"
}

type RepoModelDependencies struct {
	gorm.Model
	PkgId             uint
	Name              string
	VersionConstraint string
}

func (RepoModelDependencies) TableName() string {
	return "dependencies"
}

func RepoOpenByPath(dbPath string) (*gorm.DB, error) {
	log := logger.NewLogger("common", "database.RepoOpen")

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		log.VerboseError(err.Error())
		return nil, err
	}

	db.AutoMigrate(&RepoModelPkgs{})
	db.AutoMigrate(&RepoModelDependencies{})

	return db, nil
}

// ErrRepoNotCached means a repository has never been refreshed, so there is no
// local index to read.
var ErrRepoNotCached = errors.New("repository index has not been downloaded yet")

// RepoOpenByName opens a cached repository index by its key.
//
// A missing cache is reported rather than silently created: sqlite would
// happily make an empty database, and every query against it would return
// "package not found" with no indication that the real cause is a repository
// that was never refreshed.
func RepoOpenByName(repoName string) (*gorm.DB, error) {
	if err := utils.ValidateName(repoName); err != nil {
		return nil, err
	}

	dbPath := filepath.Join(config.RepoCacheDir(), repoName+".db")
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%s: %w", repoName, ErrRepoNotCached)
		}
		return nil, err
	}

	return RepoOpenByPath(dbPath)
}
