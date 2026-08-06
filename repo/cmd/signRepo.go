/*
Copyright © 2023 Yanick Stephan <me@yanick.gay>
*/
package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"tape/common/signing"
	commonUtils "tape/common/utils"
	"tape/repo/utils"
	"time"

	"github.com/spf13/cobra"
)

// signRepoCmd represents the sign-repo command
var signRepoCmd = &cobra.Command{
	Use:   "sign-repo [repo] [key]",
	Short: "Signs a repository index",
	Long: `Signs a repository's index with a private key.

Writes repo.db.sig alongside repo.db. Clients verify that signature against a
trusted public key before reading the index, and the index carries a digest for
every package, so one signature covers the whole repository.

Re-sign after every add-to-repo: changing the index invalidates its signature.`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		log, err := utils.CmdPrepare(cmd, "signRepo")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		pwd, err := os.Getwd()
		if err != nil {
			log.Error(err.Error())
			os.Exit(1)
		}

		repoPath := commonUtils.ResolvePath(pwd, args[0])
		keyPath := commonUtils.ResolvePath(pwd, args[1])

		repoName, err := cmd.Flags().GetString("name")
		if err != nil {
			log.Error(err.Error())
			os.Exit(1)
		}
		if repoName == "" {
			// The signature is bound to a repository name, which must match the
			// key the client knows this repository by -- that is what stops a
			// signature being replayed across repositories.
			repoName = filepath.Base(repoPath)
		}
		if err := commonUtils.ValidateName(repoName); err != nil {
			log.Error(fmt.Sprintf("repository name %q is not usable: %s", repoName, err))
			os.Exit(1)
		}

		if err := utils.SignRepo(repoPath, repoName, keyPath, time.Now()); err != nil {
			log.Error(err.Error())
			os.Exit(1)
		}

		priv, err := signing.LoadPrivateKey(keyPath)
		if err == nil {
			fmt.Printf("Signed %s as repository %q with key %s\n", filepath.Join(repoPath, "repo.db"), repoName, priv.ID)
		} else {
			fmt.Printf("Signed %s as repository %q\n", filepath.Join(repoPath, "repo.db"), repoName)
		}
	},
}

func init() {
	rootCmd.AddCommand(signRepoCmd)
	signRepoCmd.Flags().String("name", "", "Repository name the signature is bound to (defaults to the directory name)")
}
