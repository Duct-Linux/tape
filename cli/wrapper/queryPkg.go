package wrapper

import (
	"encoding/gob"
	"fmt"
	"tape/cli/utils"
	"tape/common/enums"
	"tape/common/structs"
)

// QueryPkg asks the daemon to resolve a package and returns the result.
//
// This used to be launched as `go wrapper.QueryPkg(...)` with the result
// delivered over an unbuffered channel. Any non-Done outcome skipped the
// callback that wrote to that channel, so the caller blocked forever and the Go
// runtime killed the process with an unrecoverable "all goroutines are asleep"
// deadlock. There was never any concurrency to gain: the call is synchronous.
func QueryPkg(enc *gob.Encoder, dec *gob.Decoder, pkgName string, resolveDependencies bool) ([]map[string]string, error) {
	options := map[string]interface{}{
		"resolveDependencies": resolveDependencies,
	}
	if err := enc.Encode(structs.Request{
		Type:    enums.RequestTypeQueryPkg,
		Data:    pkgName,
		Options: options,
	}); err != nil {
		return nil, err
	}

	var pkgs []map[string]string
	err := utils.UnixDecode(dec, nil, nil, nil, func(resData interface{}) {
		// Comma-ok: a daemon returning an unexpected shape must not panic the CLI.
		if resolved, ok := resData.([]map[string]string); ok {
			pkgs = resolved
		}
	})
	if err != nil {
		return nil, err
	}
	if pkgs == nil {
		return nil, fmt.Errorf("daemon returned no result for %q", pkgName)
	}

	return pkgs, nil
}

// PrintPkgs renders query results for the standalone `tape query` command.
func PrintPkgs(pkgs []map[string]string) {
	for _, pkg := range pkgs {
		if errMsg := pkg["error"]; errMsg != "" {
			fmt.Printf("== %s ==\n%s\n", pkg["name"], errMsg)
			continue
		}
		fmt.Printf("== Package: %s ==\n", pkg["name"])
		for _, key := range []string{"name", "version", "subversion", "arch", "repo"} {
			if value, ok := pkg[key]; ok {
				fmt.Printf("%s: %s\n", key, value)
			}
		}
	}
}
