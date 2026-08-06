package wrapper

import (
	"encoding/gob"
	"fmt"
	"tape/cli/utils"
	"tape/common/enums"
	"tape/common/structs"
)

// DownloadPkg asks the daemon to fetch one package and returns its cache path.
func DownloadPkg(enc *gob.Encoder, dec *gob.Decoder, pkg map[string]string) (string, error) {
	if err := enc.Encode(structs.Request{
		Type: enums.RequestTypeDownloadPkg,
		Data: pkg,
	}); err != nil {
		return "", err
	}

	var cachePath string
	err := utils.UnixDecode(dec,
		downloadPkgStartCallback,
		downloadPkgProgressCallback,
		downloadPkgProgressDoneCallback,
		func(resData interface{}) {
			if path, ok := resData.(string); ok {
				cachePath = path
			}
		},
	)
	if err != nil {
		return "", err
	}
	if cachePath == "" {
		return "", fmt.Errorf("daemon reported success but returned no package path")
	}

	return cachePath, nil
}

// The callbacks below all use comma-ok. A daemon returning a slightly different
// shape used to panic the CLI, which Cleanup then swallowed into exit status 0.

func downloadPkgStartCallback(resData interface{}) {
	name, _ := resData.(string)
	fmt.Printf("Downloading %s...\n", name)
	utils.ProgressNew("downloading")
}

func downloadPkgProgressCallback(resData interface{}) {
	utils.ProgressSet(resData)
}

func downloadPkgProgressDoneCallback(resData interface{}) {
	tmp, ok := resData.([]string)
	if !ok || len(tmp) < 2 {
		return
	}
	fmt.Printf("Downloading %s... Done! Downloaded %s\n", tmp[0], tmp[1])
}
