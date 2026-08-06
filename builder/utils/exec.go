package utils

import (
	"os"
	"os/exec"

	"github.com/spf13/viper"
)

type ExecEnv struct {
	Pwd     string
	PkgPath string
	Target  string

	BuildConfig *viper.Viper
}

func (e *ExecEnv) prepareEnv() []string {
	env := os.Environ()
	env = append(env, "TAPE_PACKAGE_NAME="+e.BuildConfig.GetString("package.name"))
	env = append(env, "TAPE_PACKAGE_VERSION="+e.BuildConfig.GetString("package.version"))
	env = append(env, "TAPE_INSTALL_DIR="+DirWorkInstall(e.PkgPath))
	env = append(env, "TAPE_TARGET="+e.Target)
	return env
}

func (e *ExecEnv) Command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Env = e.prepareEnv()

	cmd.Dir = DirWork(e.PkgPath)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	return cmd
}
