package loop

import (
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime/debug"
	"tape/common/enums"
	"tape/common/logger"
	"tape/common/structs"
	"tape/daemon/wrapper"
	"time"
)

const (
	// maxRequestBytes bounds what a single client can make the daemon
	// allocate. gob imposes no limit of its own, so a huge map would otherwise
	// OOM-kill a root process.
	maxRequestBytes = 1 << 20 // 1 MiB

	// requestReadTimeout stops a client from holding a goroutine and an fd open
	// by connecting and never sending anything (slowloris).
	requestReadTimeout = 30 * time.Second

	// handlerTimeout is the overall ceiling for serving one request. It has to
	// accommodate a full repo or package download over a slow link.
	handlerTimeout = 2 * time.Hour
)

func handleConnection(conn net.Conn) {
	log := logger.NewLogger("daemon", "loop.handleConnection")
	defer conn.Close()

	enc := gob.NewEncoder(conn)

	// A panic in a connection goroutine used to kill the entire daemon, and
	// every handler reached into client-controlled interface{} values with no
	// comma-ok. Those assertions are gone, but this stays as a backstop: one
	// malformed request must never take down the service.
	defer func() {
		if r := recover(); r != nil {
			log.Error(fmt.Sprintf("recovered from panic while handling request: %v", r))
			log.VerboseError(string(debug.Stack()))
			// Best effort -- the connection may already be unusable.
			_ = enc.Encode(structs.Response{
				Type: enums.ResponseTypeError,
				Data: "internal daemon error",
			})
		}
	}()

	if err := conn.SetReadDeadline(time.Now().Add(requestReadTimeout)); err != nil {
		log.VerboseError(err.Error())
		return
	}

	// Bound the request. Responses are written straight to conn, so only the
	// read side is capped.
	dec := gob.NewDecoder(io.LimitReader(conn, maxRequestBytes))

	var request structs.Request
	if err := dec.Decode(&request); err != nil {
		if errors.Is(err, io.EOF) {
			return
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			log.VerboseError("client disconnected mid-request")
			return
		}
		log.VerboseError(fmt.Sprintf("Error while decoding request: %s", err.Error()))
		return
	}

	// Request is in hand; give the handler a generous but finite budget so a
	// wedged mirror cannot pin the connection forever.
	if err := conn.SetDeadline(time.Now().Add(handlerTimeout)); err != nil {
		log.VerboseError(err.Error())
		return
	}

	log.VerboseInfo(fmt.Sprintf("Received data: %s", request.String()))

	if err := dispatch(&request, enc, log); err != nil {
		if encErr := enc.Encode(structs.Response{
			Type: enums.ResponseTypeError,
			Data: err.Error(),
		}); encErr != nil {
			log.VerboseError(fmt.Sprintf("failed to report error to client: %s", encErr.Error()))
		}
		log.VerboseError(fmt.Sprintf("Error while processing request: %s", err.Error()))
	}
}

// dispatch routes one request. Returning an error means "tell the client it
// failed"; handlers that already wrote a terminal response return nil.
func dispatch(request *structs.Request, enc *gob.Encoder, log *logger.Logger) error {
	switch request.Type {
	case enums.RequestTypePing:
		log.VerboseInfo("processing ping request")
		return enc.Encode(structs.Response{Type: enums.ResponseTypeDone, Data: nil})

	case enums.RequestTypeRefreshRepos:
		log.VerboseInfo("processing refreshRepos request")
		return wrapper.RefreshRepos(request, enc)

	case enums.RequestTypeQueryPkg:
		log.VerboseInfo("processing queryPkg request")
		return wrapper.QueryPkg(request, enc)

	case enums.RequestTypeDownloadPkg:
		log.VerboseInfo("processing download request")
		return wrapper.DownloadPkg(request, enc)

	case enums.RequestTypeLocalInstall:
		log.VerboseInfo("processing localInstall request")
		return wrapper.LocalInstall(request, enc)

	case enums.RequestTypeRemovePkg:
		log.VerboseInfo("processing removePkg request")
		return wrapper.RemovePkg(request, enc)

	case enums.RequestTypeListPkgs:
		log.VerboseInfo("processing listPkgs request")
		return wrapper.ListPkgs(request, enc)

	case enums.RequestTypeCheckUpgrades:
		log.VerboseInfo("processing checkUpgrades request")
		return wrapper.CheckUpgrades(request, enc)

	case enums.RequestTypeSearchPkgs:
		log.VerboseInfo("processing searchPkgs request")
		return wrapper.SearchPkgs(request, enc)

	default:
		log.VerboseInfo("request type not recognized")
		return fmt.Errorf("unknown request type: %d", request.Type)
	}
}
