package enums

type ResponseType int8

// ResponseTypeSuccess is the response type
const (
	ResponseTypeEmpty ResponseType = iota
	ResponseTypeStart
	ResponseTypeDone
	ResponseTypeError
	ResponseTypeProgress
	ResponseTypeProgressDone
)
