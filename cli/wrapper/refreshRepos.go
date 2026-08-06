package wrapper

import (
	"encoding/gob"
	"fmt"
	"tape/cli/utils"
	"tape/common/enums"
	"tape/common/structs"
)

func RefreshRepos(enc *gob.Encoder, dec *gob.Decoder, force bool) error {
	options := map[string]interface{}{
		"force": force,
	}

	err := enc.Encode(structs.Request{Type: enums.RequestTypeRefreshRepos, Options: options})
	if err != nil {
		return err
	}

	return utils.UnixDecode(dec, refreshRepoStartCallback, refreshRepoProgressCallback, refreshRepoProgressDoneCallback, refreshRepoDoneCallback)
}

func refreshRepoStartCallback(resData interface{}) {
	name, _ := resData.(string)
	fmt.Printf("Refreshing repo %s...\n", name)
	utils.ProgressNew("downloading")
}

func refreshRepoProgressCallback(resData interface{}) {
	utils.ProgressSet(resData)
}

func refreshRepoProgressDoneCallback(resData interface{}) {
	data, ok := resData.([]string)
	if !ok || len(data) < 2 {
		return
	}
	fmt.Printf("Refreshing repo %s... Done! Downloaded %s\n", data[0], data[1])
}

func refreshRepoDoneCallback(resData interface{}) {
	refreshed, _ := resData.(bool)
	if refreshed {
		fmt.Printf("All repos refreshed!\n")
	} else {
		fmt.Printf("No update available!\n")
	}
}
