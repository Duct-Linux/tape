package wrapper

import (
	"encoding/gob"
	"fmt"
	"strings"
	"tape/cli/utils"
	"tape/common/enums"
	"tape/common/structs"
)

// RemoveResult summarises what a removal did.
type RemoveResult struct {
	Name         string
	FilesRemoved string
	Orphans      []string
}

// RemovePkg asks the daemon to uninstall a package.
func RemovePkg(enc *gob.Encoder, dec *gob.Decoder, pkgName string, force bool) (*RemoveResult, error) {
	if err := enc.Encode(structs.Request{
		Type:    enums.RequestTypeRemovePkg,
		Data:    pkgName,
		Options: map[string]interface{}{"force": force},
	}); err != nil {
		return nil, err
	}

	var result *RemoveResult
	err := utils.UnixDecode(dec,
		func(resData interface{}) {
			name, _ := resData.(string)
			fmt.Printf("Removing %s...\n", name)
		},
		nil, nil,
		func(resData interface{}) {
			summary, ok := resData.(map[string]string)
			if !ok {
				return
			}
			result = &RemoveResult{
				Name:         summary["name"],
				FilesRemoved: summary["files"],
			}
			if orphans := summary["orphans"]; orphans != "" {
				result.Orphans = strings.Split(orphans, ",")
			}
		},
	)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("daemon reported success but returned no summary")
	}

	return result, nil
}

// ListPkgs asks the daemon what is installed.
func ListPkgs(enc *gob.Encoder, dec *gob.Decoder, explicitOnly, orphansOnly bool) ([]map[string]string, error) {
	if err := enc.Encode(structs.Request{
		Type: enums.RequestTypeListPkgs,
		Options: map[string]interface{}{
			"explicitOnly": explicitOnly,
			"orphansOnly":  orphansOnly,
		},
	}); err != nil {
		return nil, err
	}

	var pkgs []map[string]string
	err := utils.UnixDecode(dec, nil, nil, nil, func(resData interface{}) {
		if list, ok := resData.([]map[string]string); ok {
			pkgs = list
		}
	})
	if err != nil {
		return nil, err
	}

	return pkgs, nil
}
