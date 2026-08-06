package wrapper

import (
	"encoding/gob"
	"fmt"
	"tape/cli/utils"
	"tape/common/enums"
	"tape/common/structs"
)

func Ping(enc *gob.Encoder, dec *gob.Decoder) error {
	err := enc.Encode(structs.Request{Type: enums.RequestTypePing})
	if err != nil {
		return err
	}

	return utils.UnixDecode(dec, pingStartCallback, pingProgressCallback, pingProgressDoneCallback, pingDoneCallback)
}

func pingStartCallback(resData interface{}) {
	// nothing to do
}

func pingProgressCallback(resData interface{}) {
	// nothing to do
}

func pingProgressDoneCallback(resData interface{}) {
	// nothing to do
}

func pingDoneCallback(resData interface{}) {
	fmt.Println("The daemon is up and running!")
}
