package utils

import (
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"net"
	"tape/common/enums"
	"tape/common/global"
	"tape/common/logger"
	"tape/common/structs"
	"time"
)

// dialTimeout bounds the connect attempt so a wedged socket cannot hang the CLI.
const dialTimeout = 10 * time.Second

// DaemonError is a failure reported by the daemon, carrying the message the
// daemon actually sent.
//
// This used to be swallowed twice over: the decode loop broke out with a nil
// error -- so callers treated a failed refresh as success and carried on
// against a stale index -- and the message the daemon had carefully placed in
// Response.Data was never printed, leaving the user with "Something went wrong".
type DaemonError struct {
	Message string
}

func (e *DaemonError) Error() string {
	if e.Message == "" {
		return "the daemon reported an error"
	}
	return e.Message
}

func UnixDial() (net.Conn, *gob.Encoder, *gob.Decoder, error) {
	log := logger.NewLogger("cli", "utils.UnixDialConnection")

	socket := global.GetConfig().GetString("daemon.socket")
	conn, err := net.DialTimeout("unix", socket, dialTimeout)
	if err != nil {
		log.VerboseError(err.Error())
		return nil, nil, nil, fmt.Errorf("cannot reach the tape daemon at %s: %w", socket, err)
	}
	return conn, gob.NewEncoder(conn), gob.NewDecoder(conn), nil
}

// UnixDecode consumes the response stream for one request, dispatching to the
// callbacks, and returns once a terminal response arrives.
//
// It returns a *DaemonError when the daemon reported a failure, and an error
// when the stream ended with no terminal response at all -- the condition that
// previously left the caller blocked on a channel forever.
func UnixDecode(
	dec *gob.Decoder,
	startCallback func(resData interface{}),
	progressCallback func(resData interface{}),
	progressDoneCallback func(resData interface{}),
	doneCallback func(resData interface{}),
) error {
	for {
		// Decode into a fresh value each iteration. gob leaves fields absent
		// from a message untouched, so reusing one struct let a Done{Data:nil}
		// inherit the Data of a preceding Progress frame.
		var response structs.Response

		if err := dec.Decode(&response); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return fmt.Errorf("the daemon closed the connection without completing the request")
			}
			return err
		}

		switch response.Type {
		case enums.ResponseTypeStart:
			if startCallback != nil {
				startCallback(response.Data)
			}

		case enums.ResponseTypeProgress:
			if progressCallback != nil {
				progressCallback(response.Data)
			}

		case enums.ResponseTypeProgressDone:
			if progressDoneCallback != nil {
				progressDoneCallback(response.Data)
			}

		case enums.ResponseTypeDone:
			if doneCallback != nil {
				doneCallback(response.Data)
			}
			return nil

		case enums.ResponseTypeError:
			message, _ := response.Data.(string)
			return &DaemonError{Message: message}

		default:
			return fmt.Errorf("unknown response type: %d", response.Type)
		}
	}
}
