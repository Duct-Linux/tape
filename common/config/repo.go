package config

import (
	"os"
	"path/filepath"
	"strings"
	"tape/common/logger"
	"tape/common/utils"

	"github.com/spf13/viper"
)

func GetRepoConfigByPath(repoPath string) (*viper.Viper, error) {
	log := logger.NewLogger("common", "config.GetRepoConfig")

	repoManager := viper.New()
	repoManager.SetConfigFile(repoPath)

	err := repoManager.ReadInConfig()
	if err != nil {
		log.VerboseError(err.Error())
		return nil, err
	}

	// Strip only the final extension. Splitting on "." and taking [0] turned
	// "my.repo.toml" into the key "my".
	base := filepath.Base(repoPath)
	repoManager.Set("key", strings.TrimSuffix(base, filepath.Ext(base)))

	repoManager.SetDefault("repo.skip-tls", false)
	repoManager.SetDefault("repo.priority", 10)
	// Without a default, a repo file that omits `enabled` was silently ignored
	// by both refresh and query, with no indication why.
	repoManager.SetDefault("repo.enabled", true)
	// Signature verification is on by default. A repository that genuinely has
	// no signature has to say so explicitly, so an unsigned one is a deliberate
	// choice rather than something that happens by omission.
	repoManager.SetDefault("repo.allow-unsigned", false)

	return repoManager, nil
}

// repoDir is where repository definitions live, under the active config dir.
func repoDir() string {
	return filepath.Join(ConfigDir(), "repos")
}

func GetRepoConfigByName(repoName string) (*viper.Viper, error) {
	log := logger.NewLogger("common", "config.GetRepoConfig")

	// repoName arrives from the client. Callers validate it too, but this
	// concatenation is the exact spot where "../../tmp/evil" let an
	// unprivileged user hand the root daemon a repo config of their choosing --
	// and with it, an arbitrary baseurl and skip-tls setting.
	if err := utils.ValidateName(repoName); err != nil {
		log.VerboseError(err.Error())
		return nil, err
	}

	repoPath := filepath.Join(repoDir(), repoName+".toml")

	repoManager, err := GetRepoConfigByPath(repoPath)
	if err != nil {
		log.VerboseError(err.Error())
		return nil, err
	}

	return repoManager, nil
}

func GetAllRepos() (map[string]*viper.Viper, error) {
	log := logger.NewLogger("common", "config.GetAllRepos")

	repos := make(map[string]*viper.Viper)

	// A missing repos directory means "no repos configured", not a failure.
	entries, err := os.ReadDir(repoDir())
	if err != nil {
		if os.IsNotExist(err) {
			log.VerboseInfo(repoDir() + " does not exist; no repositories configured")
			return repos, nil
		}
		log.VerboseError(err.Error())
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		// Only .toml files are repo definitions. Handing every file to viper
		// meant a README, an editor swap file, or a .bak aborted the entire
		// walk and made refresh-repos fail outright.
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".toml") {
			log.VerboseInfo("skipping non-repo file " + entry.Name())
			continue
		}

		repo, err := GetRepoConfigByPath(filepath.Join(repoDir(), entry.Name()))
		if err != nil {
			// One malformed repo file should not hide every other repo.
			log.Warning("ignoring unreadable repo config " + entry.Name() + ": " + err.Error())
			continue
		}

		repos[repo.GetString("key")] = repo
	}

	return repos, nil
}
