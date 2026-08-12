/*
Copyright © 2023 Yanick Stephan <me@yanick.gay>
*/
package cmd

import (
	"errors"
	"os"
	"path"
	"tape/common/database"
	"tape/common/manifest"
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
				os.Exit(1)
			}
		}
		if dbPkg.ID != 0 {
			// Exit non-zero. This reported an error and exited 0, so a script
			// adding a set of packages could not tell a duplicate from a
			// success -- the publish pipeline had to re-derive the answer by
			// querying the index itself. --skip-existing is the opt-in for
			// callers that genuinely want re-running to be a no-op.
			skip, _ := cmd.Flags().GetBool("skip-existing")
			if skip {
				log.Info("Package already in repository; skipping")
				return
			}
			log.Error("Package already in repository")
			os.Exit(1)
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
			os.Exit(1)
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
			// The archive is already published at this point but nothing
			// indexes it. Exiting 0 here left the repository in that state and
			// told the caller it had worked.
			log.VerboseError(tx.Error.Error())
			os.Exit(1)
		}

		log.Info("Adding package dependencies to repository")
		// Read from the manifest file rather than through pkgConfig: viper
		// lower-cases every key it returns, and a [dependencies] key is a
		// package name. This is the second half of the same defect the builder
		// has -- fixing only the builder would change nothing, because the
		// correctly-cased names it now writes would be lower-cased again on
		// their way into the index, right here. See tape/common/manifest.
		deps, err := manifest.ReadDependencies(path.Join(tmpDir, "TAPEPACKAGE.toml"))
		if err != nil {
			log.Error(err.Error())
			os.Exit(1)
		}
		for k, v := range deps.Runtime {
			tx = repoDb.Create(&database.RepoModelDependencies{
				PkgId:             dbPkg.ID,
				Name:              k,
				VersionConstraint: v,
			})
			if tx.Error != nil {
				// A package indexed without its dependencies resolves as though
				// it had none.
				log.VerboseError(tx.Error.Error())
				os.Exit(1)
			}
		}

		log.Info("Successfully added package to repository")

		// The index just changed, so any existing signature no longer matches.
		// Removing it gives clients an accurate "not signed" rather than a
		// confusing digest mismatch.
		if err := utils.InvalidateRepoSignature(repoPath); err != nil {
			// A stale signature over a changed index is worse than no
			// signature: clients read it as tampering.
			log.Error(err.Error())
			os.Exit(1)
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
	addToRepoCmd.Flags().Bool("skip-existing", false,
		"Treat a package that is already indexed as success rather than an error")
}
