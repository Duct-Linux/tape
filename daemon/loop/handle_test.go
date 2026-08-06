package loop

import (
	"encoding/gob"
	"net"
	"os"
	"path/filepath"
	"tape/common/enums"
	"tape/common/structs"
	"testing"
	"time"
)

func init() {
	// Mirrors global.SetGlobals, which registers the concrete types carried in
	// the Request/Response interface{} fields.
	gob.Register(map[string]string{})
	gob.Register([]map[string]string{})
}

// exchange runs one request through handleConnection over an in-memory pipe and
// returns the *terminal* response, mirroring how the CLI's decode loop reads:
// it consumes Start/Progress frames and stops at Done or Error.
//
// An error here means no terminal response ever arrived -- exactly the
// condition that left `tape install` blocked on an empty channel until the Go
// runtime declared a deadlock.
func exchange(t *testing.T, req structs.Request) (*structs.Response, error) {
	t.Helper()

	client, server := net.Pipe()
	defer client.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		handleConnection(server)
	}()

	if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}

	enc := gob.NewEncoder(client)
	dec := gob.NewDecoder(client)

	if err := enc.Encode(req); err != nil {
		return nil, err
	}

	var terminal *structs.Response
	var decodeErr error
	for {
		// Decode into a fresh value each iteration: gob leaves fields absent
		// from a message untouched, so a reused struct carries stale Data
		// forward.
		var resp structs.Response
		if err := dec.Decode(&resp); err != nil {
			decodeErr = err
			break
		}
		if resp.Type == enums.ResponseTypeDone || resp.Type == enums.ResponseTypeError {
			terminal = &resp
			break
		}
	}

	client.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleConnection did not return")
	}

	if terminal == nil {
		return nil, decodeErr
	}
	return terminal, nil
}

// Each of these payloads used to hit an unchecked type assertion and panic.
// Nothing recovers a panic in a connection goroutine, so a single one of these
// took down the entire root daemon.
func TestDaemonSurvivesMalformedRequests(t *testing.T) {
	cases := []struct {
		name string
		req  structs.Request
	}{
		{
			// The five-line killer: no Options map at all.
			"queryPkg with nil data",
			structs.Request{Type: enums.RequestTypeQueryPkg},
		},
		{
			"queryPkg with int data",
			structs.Request{Type: enums.RequestTypeQueryPkg, Data: 42},
		},
		{
			"queryPkg with map data",
			structs.Request{Type: enums.RequestTypeQueryPkg, Data: map[string]string{"x": "y"}},
		},
		{
			"queryPkg with traversal name",
			structs.Request{Type: enums.RequestTypeQueryPkg, Data: "../../etc/passwd"},
		},
		{
			"downloadPkg with nil data",
			structs.Request{Type: enums.RequestTypeDownloadPkg},
		},
		{
			"downloadPkg with string data",
			structs.Request{Type: enums.RequestTypeDownloadPkg, Data: "not a map"},
		},
		{
			"downloadPkg with empty map",
			structs.Request{Type: enums.RequestTypeDownloadPkg, Data: map[string]string{}},
		},
		{
			// The arbitrary-root-file-write payload.
			"downloadPkg with traversal name",
			structs.Request{
				Type: enums.RequestTypeDownloadPkg,
				Data: map[string]string{
					"repo":       "core",
					"name":       "../../../../etc/cron.d/pwn",
					"version":    "1.0",
					"subversion": "1",
					"arch":       "x86_64",
				},
			},
		},
		{
			"downloadPkg with traversal repo",
			structs.Request{
				Type: enums.RequestTypeDownloadPkg,
				Data: map[string]string{
					"repo":       "../../tmp/evil",
					"name":       "bash",
					"version":    "1.0",
					"subversion": "1",
					"arch":       "x86_64",
				},
			},
		},
		{
			"unknown request type",
			structs.Request{Type: enums.RequestType(99)},
		},
		{
			"empty request type",
			structs.Request{Type: enums.RequestTypeEmpty},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := exchange(t, tc.req)
			if err != nil {
				t.Fatalf("no response from daemon (client would block forever): %v", err)
			}
			if resp.Type != enums.ResponseTypeError {
				t.Errorf("response type = %v, want ResponseTypeError", resp.Type)
			}
			if msg, ok := resp.Data.(string); !ok || msg == "" {
				t.Errorf("error response carried no message: %#v", resp.Data)
			}
		})
	}
}

// Every request type must produce a terminal response. The CLI's decode loop
// exits only on Done or Error, so an unhandled type made `tape install` block
// on an empty channel until the Go runtime declared a deadlock.
func TestEveryRequestTypeGetsATerminalResponse(t *testing.T) {
	types := []enums.RequestType{
		enums.RequestTypeEmpty,
		enums.RequestTypePing,
		enums.RequestTypeQueryPkg,
		enums.RequestTypeDownloadPkg,
		enums.RequestTypeRefreshRepos,
		enums.RequestTypeLocalInstall,
		enums.RequestTypeRemovePkg,
		enums.RequestTypeListPkgs,
		enums.RequestTypeCheckUpgrades,
		enums.RequestTypeSearchPkgs,
	}

	for _, rt := range types {
		t.Run(string(rune('0'+rt)), func(t *testing.T) {
			resp, err := exchange(t, structs.Request{Type: rt})
			if err != nil {
				t.Fatalf("request type %v produced no response: %v", rt, err)
			}
			if resp.Type != enums.ResponseTypeDone && resp.Type != enums.ResponseTypeError {
				t.Errorf("request type %v: first response %v is not terminal", rt, resp.Type)
			}
		})
	}
}

// A well-formed request carrying a garbage option is not an error: the option
// degrades to its default. What matters is that the daemon neither panics nor
// goes silent -- it must still reach a terminal response.
func TestMalformedOptionDegradesInsteadOfCrashing(t *testing.T) {
	resp, err := exchange(t, structs.Request{
		Type:    enums.RequestTypeQueryPkg,
		Data:    "bash",
		Options: map[string]interface{}{"resolveDependencies": "yes please"},
	})
	if err != nil {
		t.Fatalf("no terminal response (client would block forever): %v", err)
	}
	if resp.Type != enums.ResponseTypeDone && resp.Type != enums.ResponseTypeError {
		t.Errorf("response %v is not terminal", resp.Type)
	}
}

func TestPingStillWorks(t *testing.T) {
	resp, err := exchange(t, structs.Request{Type: enums.RequestTypePing})
	if err != nil {
		t.Fatalf("ping failed: %v", err)
	}
	if resp.Type != enums.ResponseTypeDone {
		t.Errorf("ping response = %v, want Done", resp.Type)
	}
}

// A client names the archive to install, so the daemon must refuse anything
// outside its own download cache -- otherwise an unauthenticated caller could
// point the root daemon at an archive they wrote themselves.
func TestLocalInstallConfinesPathsToTheCache(t *testing.T) {
	hostile := []string{
		"/etc/passwd",
		"/home/attacker/evil.tape.tar.gz",
		"../../etc/evil.tape.tar.gz",
		"relative/path.tape.tar.gz",
		"",
		// Inside the cache root but not a package archive.
		filepath.Join(os.TempDir(), "tape", "notapackage.txt"),
		// Prefix-matching trap: a sibling directory whose name starts the same.
		filepath.Join(os.TempDir(), "tape-evil", "pkg.tape.tar.gz"),
	}

	for _, path := range hostile {
		t.Run(path, func(t *testing.T) {
			resp, err := exchange(t, structs.Request{
				Type: enums.RequestTypeLocalInstall,
				Data: path,
			})
			if err != nil {
				t.Fatalf("no response from daemon: %v", err)
			}
			if resp.Type != enums.ResponseTypeError {
				t.Errorf("localInstall(%q) = %v, want Error", path, resp.Type)
			}
		})
	}
}
