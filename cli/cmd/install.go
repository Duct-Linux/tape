/*
Copyright © 2023 Yanick Stephan <me@yanick.gay>
*/
package cmd

import (
	"fmt"
	"tape/cli/utils"
	"tape/cli/wrapper"
	commonUtils "tape/common/utils"

	"github.com/fatih/color"
	"github.com/rodaine/table"
	"github.com/spf13/cobra"
)

// installCmd represents the install command
var installCmd = &cobra.Command{
	Use:   "install [package...]",
	Short: "Installs a package",
	Long:  `Installs a package from a repository.`,
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		log, daemon, err := utils.CmdPrepare(cmd, "install")
		if err != nil {
			// CmdPrepare failed before we have anything to clean up.
			utils.Fail(log, err)
			utils.Cleanup(nil, daemon)
			return
		}

		// One cleanup for the whole command. Registering `defer Recover(&conn,
		// ...)` per loop iteration was pointless: Cleanup calls os.Exit, which
		// skips every remaining defer, so at most one of them ever ran -- and
		// each held the same &conn, leaking a socket per package. Connections
		// are now closed at their point of use instead.
		if err := runInstall(cmd, args); err != nil {
			utils.Fail(log, err)
		}

		utils.Cleanup(nil, daemon)
	},
}

// runInstall performs the install transaction. Each step opens its own
// connection and closes it before the next one, so nothing leaks even though
// the daemon serves one request per connection.
func runInstall(cmd *cobra.Command, args []string) error {
	// Refreshing every index before every install is the right default -- it
	// is how a stale cache stops silently pinning old versions -- but it was
	// also the only behaviour, so an install could not proceed without network
	// access to every configured repository. The cache on disk is perfectly
	// usable for installing something already indexed.
	noRefresh, err := cmd.Flags().GetBool("no-refresh")
	if err != nil {
		return err
	}
	if !noRefresh {
		if err := refreshBeforeInstall(); err != nil {
			return fmt.Errorf("refreshing repositories: %w", err)
		}
	}

	foundPkgs, err := resolvePackages(args)
	if err != nil {
		return err
	}

	// Reject the whole transaction if any requested package is missing, before
	// downloading anything.
	for _, pkg := range foundPkgs {
		if errMsg := pkg["error"]; errMsg != "" {
			return fmt.Errorf("%s: %s", pkg["name"], errMsg)
		}
	}

	printPkgTable(foundPkgs)

	confirm, err := cmd.Flags().GetBool("yes")
	if err != nil {
		return err
	}
	if !confirm {
		confirm = utils.Confirm("Do you want to install these packages?")
	}
	if !confirm {
		fmt.Println("Aborting...")
		return nil
	}

	fmt.Println("Downloading packages...")
	paths, err := downloadPackages(foundPkgs)
	if err != nil {
		return err
	}

	// Packages the user named are explicit; everything the resolver pulled in
	// is a dependency, and only dependencies can later become orphans.
	//
	// Folded on both sides: the resolver answers with the name as the index
	// spells it, so `tape install libx11` would otherwise come back as "libX11",
	// miss this set, and be recorded as a dependency -- an orphan the moment it
	// is installed, which `tape remove --orphans` would then offer to delete.
	explicit := make(map[string]struct{}, len(args))
	for _, name := range args {
		explicit[commonUtils.FoldName(name)] = struct{}{}
	}

	return installPackages(paths, foundPkgs, explicit)
}

func refreshBeforeInstall() error {
	conn, enc, dec, err := utils.UnixDial()
	if err != nil {
		return err
	}
	defer conn.Close()

	return wrapper.RefreshRepos(enc, dec, false)
}

func resolvePackages(names []string) ([]map[string]string, error) {
	foundPkgs := make([]map[string]string, 0, len(names))

	for _, pkgName := range names {
		conn, enc, dec, err := utils.UnixDial()
		if err != nil {
			return nil, err
		}

		pkgs, err := wrapper.QueryPkg(enc, dec, pkgName, true)
		conn.Close()
		if err != nil {
			return nil, fmt.Errorf("resolving %s: %w", pkgName, err)
		}
		if len(pkgs) == 0 {
			return nil, fmt.Errorf("package %s not found", pkgName)
		}

		foundPkgs = append(foundPkgs, pkgs...)
	}

	return dedupePkgs(foundPkgs), nil
}

// dedupePkgs collapses the duplicates that diamond dependencies produce. The
// resolver walks the graph without a visited set, so a package depended on by
// two others comes back twice -- and used to be downloaded and installed twice.
func dedupePkgs(pkgs []map[string]string) []map[string]string {
	seen := make(map[string]struct{}, len(pkgs))
	unique := make([]map[string]string, 0, len(pkgs))

	for _, pkg := range pkgs {
		// The name is folded into the key so two spellings of one package
		// collapse to a single download and a single install.
		key := pkg["repo"] + "/" + commonUtils.FoldName(pkg["name"]) + "-" + pkg["version"] + "-" + pkg["subversion"] + "." + pkg["arch"]
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, pkg)
	}

	return unique
}

func printPkgTable(pkgs []map[string]string) {
	headerFmt := color.New(color.FgGreen, color.Underline).SprintfFunc()
	columnFmt := color.New(color.FgYellow).SprintfFunc()

	pkgsTbl := table.New("Name", "Version", "Arch", "Repository")
	pkgsTbl.WithHeaderFormatter(headerFmt).WithFirstColumnFormatter(columnFmt)

	for _, pkg := range pkgs {
		pkgsTbl.AddRow(pkg["name"], pkg["version"]+"-"+pkg["subversion"], pkg["arch"], pkg["repo"])
	}

	fmt.Println("Found packages:")
	pkgsTbl.Print()
}

func downloadPackages(pkgs []map[string]string) ([]string, error) {
	paths := make([]string, 0, len(pkgs))

	for _, pkg := range pkgs {
		conn, enc, dec, err := utils.UnixDial()
		if err != nil {
			return nil, err
		}

		path, err := wrapper.DownloadPkg(enc, dec, pkg)
		conn.Close()
		if err != nil {
			return nil, fmt.Errorf("downloading %s: %w", pkg["name"], err)
		}

		paths = append(paths, path)
	}

	return paths, nil
}

// installPackages installs dependencies before the packages that need them, so
// a failure part-way through never leaves a package installed without its
// dependencies present.
func installPackages(paths []string, pkgs []map[string]string, explicit map[string]struct{}) error {
	type job struct {
		path         string
		name         string
		repo         string
		asDependency bool
	}

	jobs := make([]job, 0, len(paths))
	for i, path := range paths {
		name, repo := "", ""
		if i < len(pkgs) {
			name, repo = pkgs[i]["name"], pkgs[i]["repo"]
		}
		_, isExplicit := explicit[commonUtils.FoldName(name)]
		jobs = append(jobs, job{path: path, name: name, repo: repo, asDependency: !isExplicit})
	}

	// Dependencies first. resolvePackages already emits them ahead of the
	// package that pulled them in, so a stable partition preserves that order.
	ordered := make([]job, 0, len(jobs))
	for _, j := range jobs {
		if j.asDependency {
			ordered = append(ordered, j)
		}
	}
	for _, j := range jobs {
		if !j.asDependency {
			ordered = append(ordered, j)
		}
	}

	for _, j := range ordered {
		conn, enc, dec, err := utils.UnixDial()
		if err != nil {
			return err
		}

		err = wrapper.LocalInstall(enc, dec, j.path, j.asDependency, j.repo)
		conn.Close()
		if err != nil {
			return fmt.Errorf("installing %s: %w", j.name, err)
		}
	}

	fmt.Println("Done!")
	return nil
}

func init() {
	rootCmd.AddCommand(installCmd)
	installCmd.Flags().BoolP("yes", "y", false, "Skip confirmation")
	installCmd.Flags().Bool("no-refresh", false, "Install from the cached repository indexes without refreshing them first")
}
