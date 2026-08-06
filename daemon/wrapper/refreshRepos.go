package wrapper

import (
	"encoding/gob"
	"tape/common/config"
	"tape/common/enums"
	"tape/common/logger"
	"tape/common/structs"
	"tape/daemon/utils"
)

func RefreshRepos(request *structs.Request, enc *gob.Encoder) error {
	log := logger.NewLogger("daemon", "wrapper.RefreshRepos")

	var err error

	repos, err := config.GetAllRepos()
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}

	// A request with no Options map at all used to panic here, taking the whole
	// root daemon down with it.
	force := request.BoolOptionOr("force", false)
	if !force {
		// check last update
		shouldUpdate, err := utils.CheckLastUpdateTime()
		if err != nil {
			log.VerboseError(err.Error())
			return err
		}

		if !shouldUpdate {
			// no update available
			err = enc.Encode(structs.Response{Type: enums.ResponseTypeDone, Data: false})
			if err != nil {
				log.VerboseError(err.Error())
				return err
			}

			return nil
		}
	}

	for _, repo := range repos {
		if repo.GetBool("repo.enabled") {
			err = enc.Encode(structs.Response{Type: enums.ResponseTypeStart, Data: repo.GetString("repo.name")})
			if err != nil {
				log.VerboseError(err.Error())
				return err
			}

			size, err := utils.RefreshRepo(repo, utils.UpdateProgress(enc))
			if err != nil {
				log.VerboseError(err.Error())
				return err
			}

			err = enc.Encode(structs.Response{Type: enums.ResponseTypeProgressDone, Data: []string{repo.GetString("repo.name"), size}})
			if err != nil {
				log.VerboseError(err.Error())
				return err
			}
		}
	}

	// update last update time
	err = utils.WriteLastUpdateTime()
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}

	err = enc.Encode(structs.Response{Type: enums.ResponseTypeDone, Data: true})
	if err != nil {
		log.VerboseError(err.Error())
		return err
	}

	return nil
}
