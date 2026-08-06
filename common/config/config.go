package config

import (
	"os"
	"path/filepath"
	"tape/common/global"
	"tape/common/logger"

	"github.com/spf13/viper"
)

// ConfigDir is where tape reads its configuration.
//
// TAPE_CONFIG_DIR overrides the system location, which is what makes it
// possible to run the daemon against a chroot or a throwaway tree without
// touching the running system. It is read from the environment of whoever
// starts the process; since neither binary is setuid, this grants no privilege
// its caller does not already have.
func ConfigDir() string {
	if dir := os.Getenv("TAPE_CONFIG_DIR"); dir != "" {
		return dir
	}
	return "/etc/tape"
}

// CacheDir is where downloaded repository indexes live.
//
// Resolved from TAPE_CACHE_DIR, then the loaded configuration, then the system
// default -- so a dev or chroot setup can keep its cache out of /var.
func CacheDir() string {
	if dir := os.Getenv("TAPE_CACHE_DIR"); dir != "" {
		return dir
	}
	if cfg := global.GetConfig(); cfg != nil {
		if dir := cfg.GetString("daemon.cache-dir"); dir != "" {
			return dir
		}
	}
	return "/var/cache/tape"
}

// KeysDir holds the public keys this system trusts to sign repositories.
func KeysDir() string {
	return filepath.Join(ConfigDir(), "keys")
}

// RepoCacheDir is where the per-repository index databases are stored.
func RepoCacheDir() string {
	return filepath.Join(CacheDir(), "repos")
}

func GetConfigManager() (*viper.Viper, error) {
	log := logger.NewLogger("common", "config.GetConfigManager")

	configManager := viper.New()
	configManager.SetConfigName("config")
	configManager.SetConfigType("toml")
	configManager.AddConfigPath(ConfigDir())

	addDefaults(configManager)

	err := configManager.ReadInConfig()
	if err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			log.Info("Config file not found")
		} else {
			log.VerboseError("Config file found but another error occurred")
			return nil, err
		}
	}

	return configManager, nil
}

func addDefaults(configManager *viper.Viper) {
	configManager.SetDefault("daemon.socket", "/var/run/tape.sock")
	configManager.SetDefault("daemon.pid", "/var/run/tape.pid")
	configManager.SetDefault("daemon.log", "/var/log/tape.log")
	configManager.SetDefault("daemon.skip-tls", false)

	// The daemon runs privileged and authenticates nobody, so its socket must
	// not be reachable by unprivileged processes. net.Listen would otherwise
	// create it as 0777&^umask -- world-connectable under root's usual umask.
	// Widen this only once peer-credential checks exist.
	configManager.SetDefault("daemon.socket-mode", 0660)

	// Where packages are installed and what is installed there. sysroot is
	// overridable so the daemon can populate a chroot or be exercised under
	// test without touching the running system.
	configManager.SetDefault("daemon.sysroot", "/")
	configManager.SetDefault("daemon.installed-db", "/var/lib/tape/installed.db")
	configManager.SetDefault("daemon.cache-dir", "/var/cache/tape")

	configManager.SetDefault("cli.daemon-start", false) // this one is for chroot environments
}
