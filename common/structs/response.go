package structs

import (
	"tape/common/enums"
)

type Response struct {
	Type enums.ResponseType
	Data interface{}
}
