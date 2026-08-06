/*
Copyright © 2023 Yanick Stephan <me@yanick.gay>
*/
package cmd

import (
	"fmt"
	"os"
	"tape/common/signing"
	commonUtils "tape/common/utils"
	"tape/repo/utils"

	"github.com/spf13/cobra"
)

// generateKeyCmd represents the generate-key command
var generateKeyCmd = &cobra.Command{
	Use:   "generate-key [path]",
	Short: "Generates a repository signing key",
	Long: `Generates an ed25519 keypair for signing repositories.

Two files are written: <path> holds the private key (mode 0600) and
<path>.pub holds the public key. Distribute the public key to the machines
that will install from the repository, by placing it in their
/etc/tape/keys directory. Keep the private key off those machines.

The private key is stored unencrypted, so its file permissions are the only
thing protecting it.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		log, err := utils.CmdPrepare(cmd, "generateKey")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		pwd, err := os.Getwd()
		if err != nil {
			log.Error(err.Error())
			os.Exit(1)
		}
		keyPath := commonUtils.ResolvePath(pwd, args[0])

		priv, err := signing.GenerateKey()
		if err != nil {
			log.Error(err.Error())
			os.Exit(1)
		}

		// Refuses to overwrite: replacing a signing key invalidates every
		// repository already signed with it.
		if err := signing.WritePrivateKey(keyPath, priv); err != nil {
			log.Error(err.Error())
			os.Exit(1)
		}
		if err := signing.WritePublicKey(keyPath+".pub", priv.Public()); err != nil {
			log.Error(err.Error())
			os.Exit(1)
		}

		fmt.Printf("Key id:      %s\n", priv.ID)
		fmt.Printf("Private key: %s (keep this secret)\n", keyPath)
		fmt.Printf("Public key:  %s\n", keyPath+".pub")
		fmt.Println()
		fmt.Printf("Install the public key on client machines as:\n  /etc/tape/keys/%s.pub\n", priv.ID)
	},
}

func init() {
	rootCmd.AddCommand(generateKeyCmd)
}
