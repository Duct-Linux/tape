package buildsteps

import (
	"path"
	"tape/common/logger"
)

func Stage4Install() error {
	log := logger.NewLogger("builder", "buildsteps.Stage4Install")

	if buildConfig.IsSet("install.script") {
		cmd := execEnv.Command(path.Join(pkgPath, buildConfig.GetString("install.script")))
		err := cmd.Run()
		if err != nil {
			log.VerboseError(err.Error())
			return err
		}
	}
	return nil
}
