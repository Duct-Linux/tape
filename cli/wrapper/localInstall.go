package wrapper

import (
	"encoding/gob"
	"fmt"
	"tape/cli/utils"
	"tape/common/enums"
	"tape/common/structs"
)

// LocalInstall asks the daemon to install an already-downloaded package.
//
// asDependency records why the package is being installed, which is what makes
// orphan cleanup possible later: a package pulled in only to satisfy a
// dependency becomes removable once nothing needs it.
func LocalInstall(enc *gob.Encoder, dec *gob.Decoder, path string, asDependency bool, repo string) error {
	if err := enc.Encode(structs.Request{
		Type:    enums.RequestTypeLocalInstall,
		Data:    path,
		Options: map[string]interface{}{"asDependency": asDependency, "repo": repo},
	}); err != nil {
		return err
	}

	return utils.UnixDecode(dec,
		localInstallStartCallback,
		localInstallProgressCallback,
		localInstallProgressDoneCallback,
		nil,
	)
}

func localInstallStartCallback(resData interface{}) {
	name, _ := resData.(string)
	fmt.Printf("Installing %s...\n", name)
	utils.ProgressNew("installing")
}

func localInstallProgressCallback(resData interface{}) {
	utils.ProgressSet(resData)
}

func localInstallProgressDoneCallback(resData interface{}) {
	tmp, ok := resData.([]string)
	if !ok || len(tmp) < 1 {
		return
	}
	fmt.Printf("Installing %s... Done!\n", tmp[0])
}
