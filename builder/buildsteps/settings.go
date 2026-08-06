package buildsteps

import (
	"tape/builder/utils"
	"tape/common/arch"

	"github.com/spf13/viper"
)

var (
	// settings
	pkgPath     string
	buildConfig *viper.Viper

	// exec
	execEnv utils.ExecEnv

	// wrap settings
	out string

	// targetArch is the architecture stamped into the built package. It comes
	// from --target, so a cross build is labelled for the machine it is for
	// rather than the machine it ran on.
	targetArch string
)

func SetSettings(pwd string, target string, pkgPathArg string, buildConfigArg *viper.Viper, outArg string) {
	pkgPath = pkgPathArg
	buildConfig = buildConfigArg
	out = outArg
	targetArch = arch.Normalize(target)
	execEnv = utils.ExecEnv{
		Pwd:     pwd,
		PkgPath: pkgPath,
		Target:  target,

		BuildConfig: buildConfig,
	}
}
