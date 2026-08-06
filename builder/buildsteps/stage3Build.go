package buildsteps

import (
	"path"
	"tape/common/logger"
)

func Stage3Build() error {
	log := logger.NewLogger("builder", "buildsteps.Stage3Build")

	if buildConfig.IsSet("build.script") {
		cmd := execEnv.Command(path.Join(pkgPath, buildConfig.GetString("build.script")))
		err := cmd.Run()
		if err != nil {
			log.VerboseError(err.Error())
			return err
		}
	}
	return nil
}
