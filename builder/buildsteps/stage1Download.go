package buildsteps

import (
	"path"
	"tape/common/logger"
)

func Stage1Download() error {
	log := logger.NewLogger("builder", "buildsteps.Stage1Download")

	if buildConfig.IsSet("source.script") {
		cmd := execEnv.Command(path.Join(pkgPath, buildConfig.GetString("source.script")))
		err := cmd.Run()
		if err != nil {
			log.VerboseError(err.Error())
			return err
		}
	}
	return nil
}
