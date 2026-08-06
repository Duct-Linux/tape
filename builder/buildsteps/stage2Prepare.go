package buildsteps

import (
	"path"
	"tape/common/logger"
)

func Stage2Prepare() error {
	log := logger.NewLogger("builder", "buildsteps.Stage2Prepare")

	if buildConfig.IsSet("prepare.script") {
		cmd := execEnv.Command(path.Join(pkgPath, buildConfig.GetString("prepare.script")))
		err := cmd.Run()
		if err != nil {
			log.VerboseError(err.Error())
			return err
		}
	}
	return nil
}
