package utils

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"
	"tape/common/config"
	"tape/common/logger"
	commonUtils "tape/common/utils"
	"time"

	"github.com/spf13/viper"
)

func RefreshRepo(repo *viper.Viper, progressUpdate func(int8)) (string, error) {
	log := logger.NewLogger("daemon", "utils.RefreshRepo")

	baseUrl := repo.GetString("repo.baseurl")
	repoKey := repo.GetString("key")
	skipTls := repo.GetBool("repo.skip-tls")

	repoDbUrl, err := joinRepoLocation(baseUrl, RepoDbName)
	if err != nil {
		log.VerboseError(err.Error())
		return "", err
	}

	repoDbPath := path.Join(config.RepoCacheDir(), repoKey+".db")

	// DownloadFile used to create this implicitly; staging happens first now, so
	// the cache directory has to exist before we can put anything in it.
	if err := os.MkdirAll(config.RepoCacheDir(), 0755); err != nil {
		log.VerboseError(err.Error())
		return "", err
	}

	// Download into a staging directory rather than straight over the cached
	// index. The copy already on disk has been verified; an unverified download
	// must not be allowed to replace it, even briefly.
	staging, err := os.MkdirTemp(config.RepoCacheDir(), ".refresh-"+repoKey+"-")
	if err != nil {
		log.VerboseError(err.Error())
		return "", err
	}
	defer os.RemoveAll(staging)

	stagedDb := path.Join(staging, RepoDbName)
	size, err := commonUtils.DownloadFile(repoDbUrl, stagedDb, skipTls, progressUpdate)
	if err != nil {
		log.VerboseError(err.Error())
		return "", err
	}

	// The signature is optional to fetch but, unless the repository is
	// explicitly marked allow-unsigned, mandatory to verify.
	stagedSig := path.Join(staging, RepoSigName)
	if sigUrl, err := joinRepoLocation(baseUrl, RepoSigName); err == nil {
		if _, err := commonUtils.DownloadFile(sigUrl, stagedSig, skipTls, nil); err != nil {
			log.VerboseInfo("no signature published for " + repoKey + ": " + err.Error())
			os.Remove(stagedSig)
		}
	}

	if err := VerifyRepoIndex(repo, stagedDb, stagedSig); err != nil {
		log.Error(err.Error())
		return "", err
	}

	// Verified: promote it to the cache. Rename within the cache directory, so
	// this is atomic and readers never see a partial index.
	if err := os.Rename(stagedDb, repoDbPath); err != nil {
		log.VerboseError(err.Error())
		return "", err
	}

	return commonUtils.ConvertBytesToHumanReadable(size), nil
}

// joinRepoLocation builds a URL for a file in a repository, handling the local
// directory baseurls the dev repos use, which url.JoinPath would mangle.
func joinRepoLocation(baseUrl, name string) (string, error) {
	if baseUrl == "" {
		return "", fmt.Errorf("repo has no baseurl configured")
	}
	if strings.HasPrefix(baseUrl, "/") {
		return path.Join(baseUrl, name), nil
	}
	return url.JoinPath(baseUrl, name)
}

func WriteLastUpdateTime() error {
	log := logger.NewLogger("daemon", "utils.WriteLastUpdateTime")

	timeFile := path.Join(config.RepoCacheDir(), "last_update")
	err := os.MkdirAll(path.Dir(timeFile), os.ModePerm)
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}

	timeString := time.Now().Format(time.RFC3339)
	log.VerboseInfo("Writing timestamp to file: " + timeString)

	// Written atomically. os.Create followed by WriteString left a 0-byte file
	// if the daemon died between the two, and GetLastUpdateTime then failed
	// forever on that empty file -- bricking refresh until someone deleted it
	// by hand.
	tmp, err := os.CreateTemp(path.Dir(timeFile), ".last_update-")
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.WriteString(timeString); err != nil {
		tmp.Close()
		log.VerboseError(err.Error())
		return err
	}
	if err := tmp.Close(); err != nil {
		log.VerboseError(err.Error())
		return err
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		log.VerboseError(err.Error())
		return err
	}

	return os.Rename(tmpName, timeFile)
}

func GetLastUpdateTime() (time.Time, error) {
	log := logger.NewLogger("daemon", "utils.GetLastUpdateTime")
	timeFile := path.Join(config.RepoCacheDir(), "last_update")
	// A missing, empty, or corrupt marker means "never refreshed", not a hard
	// error. Treating it as an error made a single truncated write permanently
	// fatal. Reading with os.ReadFile also avoids the unchecked partial read
	// into a fixed 100-byte buffer.
	raw, err := os.ReadFile(timeFile)
	if err != nil {
		if os.IsNotExist(err) {
			log.VerboseInfo("no last_update marker yet")
			return time.Time{}, nil
		}
		log.VerboseError(err.Error())
		return time.Time{}, err
	}

	timeString := strings.TrimSpace(string(raw))
	if timeString == "" {
		log.VerboseWarning("last_update marker is empty; treating as never refreshed")
		return time.Time{}, nil
	}
	log.VerboseInfo("Read timestamp from file: " + timeString)

	parsed, err := time.Parse(time.RFC3339, timeString)
	if err != nil {
		log.VerboseWarning("last_update marker is unparseable; treating as never refreshed")
		return time.Time{}, nil
	}

	return parsed, nil
}

func CheckLastUpdateTime() (bool, error) {
	log := logger.NewLogger("daemon", "utils.CheckLastUpdateTime")

	lastUpdate, err := GetLastUpdateTime()
	if err != nil {
		log.VerboseError(err.Error())
		return false, err
	}

	// check if last update was more than 1 hour ago
	if time.Since(lastUpdate).Hours() > 1 {
		log.VerboseInfo("Last update was more than 1 hour ago")
		return true, nil
	}

	return false, nil
}
