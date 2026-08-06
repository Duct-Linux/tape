/*
Copyright © 2023 Yanick Stephan <me@yanick.gay>
*/
package cmd

import (
	"errors"
	"os"
	"path"
	"tape/common/database"
	commonUtils "tape/common/utils"
	"tape/repo/utils"

	"github.com/spf13/cobra"
	"gorm.io/gorm"
)

// addToRepoCmd represents the addToRepo command
var addToRepoCmd = &cobra.Command{
	Use:   "add-to-repo [repo] [package]",
	Short: "Add a package to a repository",
	Long:  `Add a package to a repository`,
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		log, err := utils.CmdPrepare(cmd, "addToRepo")
		if err != nil {
			log.Error(err.Error())
			os.Exit(1)
		}

		pwd, err := os.Getwd()
		if err != nil {
			log.Error(err.Error())
			os.Exit(1)
		}

		repoPath := commonUtils.ResolvePath(pwd, args[0])
		pkgPath := commonUtils.ResolvePath(pwd, args[1])

		log.Info("Opening repository")
		repoDbPath := path.Join(repoPath, "repo.db")
		repoDb, err := database.RepoOpenByPath(repoDbPath)
		if err != nil {
			log.Error(err.Error())
			os.Exit(1)
		}

		log.Info("Opening package")
		tmpDir, pkgConfig, err := utils.PkgOpen(pkgPath)
		if err != nil {
			log.Error(err.Error())
			os.Exit(1)
		}

		log.Info("Checking if package is already in repository")
		var dbPkg database.RepoModelPkgs
		tx := repoDb.Where("name = ? AND version = ? AND subversion = ? AND arch = ?", pkgConfig.GetString("package.name"), pkgConfig.GetString("package.version"), pkgConfig.GetString("package.subversion"), pkgConfig.GetString("package.arch")).First(&dbPkg)
		if tx.Error != nil {
			if !errors.Is(tx.Error, gorm.ErrRecordNotFound) {
				log.VerboseError(tx.Error.Error())
				return
			}
		}
		if dbPkg.ID != 0 {
			log.Error("Package already in repository")
			return
		}

		// Publish the archive first. PkgCopy re-tars the extracted contents
		// rather than copying the input file, so the published artifact is a
		// different sequence of bytes -- digesting the input here would record a
		// hash that never matches what a client downloads.
		log.Info("Copying package to repository")
		publishedPath, err := utils.PkgCopy(tmpDir, pkgConfig, repoPath)
		if err != nil {
			log.Error(err.Error())
			os.Exit(1)
		}

		// Digest of the archive as published. Recording it in the index is what
		// lets a client verify a downloaded package: the index itself is signed,
		// so a digest inside it is trustworthy.
		digest, size, err := utils.PkgDigest(publishedPath)
		if err != nil {
			log.Error(err.Error())
			return
		}

		log.Info("Adding package to repository")
		dbPkg = database.RepoModelPkgs{
			Name:       pkgConfig.GetString("package.name"),
			Version:    pkgConfig.GetString("package.version"),
			Subversion: pkgConfig.GetString("package.subversion"),
			Arch:       pkgConfig.GetString("package.arch"),
			Sha256:     digest,
			Size:       size,
		}
		tx = repoDb.Create(&dbPkg)
		if tx.Error != nil {
			log.VerboseError(tx.Error.Error())
			return
		}

		log.Info("Adding package dependencies to repository")
		dep := pkgConfig.GetStringMap("dependencies")
		for k, v := range dep {
			if k == "build" {
				continue
			}

			tx = repoDb.Create(&database.RepoModelDependencies{
				PkgId:             dbPkg.ID,
				Name:              k,
				VersionConstraint: v.(string),
			})
			if tx.Error != nil {
				log.VerboseError(tx.Error.Error())
				return
			}
		}

		log.Info("Successfully added package to repository")

		// The index just changed, so any existing signature no longer matches.
		// Removing it gives clients an accurate "not signed" rather than a
		// confusing digest mismatch.
		if err := utils.InvalidateRepoSignature(repoPath); err != nil {
			log.Error(err.Error())
			return
		}
		log.Warning("repository index changed: re-run sign-repo before publishing")

		log.Info("Cleaning up")
		err = os.RemoveAll(tmpDir)
		if err != nil {
			log.Error(err.Error())
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(addToRepoCmd)
}
