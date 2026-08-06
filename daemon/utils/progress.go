package utils

import (
	"encoding/gob"
	"tape/common/enums"
	"tape/common/structs"
)

func UpdateProgress(enc *gob.Encoder) func(int8) {
	return func(progress int8) {
		enc.Encode(structs.Response{
			Type: enums.ResponseTypeProgress,
			Data: progress,
		})
	}
}
