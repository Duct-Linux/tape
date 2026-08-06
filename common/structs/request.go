package structs

import (
	"fmt"
	"tape/common/enums"
)

type Request struct {
	Type    enums.RequestType
	Data    interface{}
	Options map[string]interface{}
}

// Data and Options are untyped because they cross a gob boundary, so every
// read of them is a decision about untrusted input. The accessors below return
// errors instead of panicking: a handler that reached for request.Data.(string)
// directly would take the whole root daemon down on a malformed request, since
// nothing recovers a panic in a connection goroutine.

// StringData returns Data as a non-empty string.
func (r *Request) StringData() (string, error) {
	s, ok := r.Data.(string)
	if !ok {
		return "", fmt.Errorf("request data must be a string, got %T", r.Data)
	}
	if s == "" {
		return "", fmt.Errorf("request data must not be empty")
	}
	return s, nil
}

// StringMapData returns Data as a non-empty map[string]string.
func (r *Request) StringMapData() (map[string]string, error) {
	m, ok := r.Data.(map[string]string)
	if !ok {
		return nil, fmt.Errorf("request data must be a map[string]string, got %T", r.Data)
	}
	if len(m) == 0 {
		return nil, fmt.Errorf("request data map must not be empty")
	}
	return m, nil
}

// BoolOptionOr reads a boolean option, falling back when the Options map is
// nil, the key is absent, or the value is not a bool. Options are advisory, so
// a malformed one degrades to the default rather than failing the request.
func (r *Request) BoolOptionOr(key string, fallback bool) bool {
	if r.Options == nil {
		return fallback
	}
	value, present := r.Options[key]
	if !present {
		return fallback
	}
	b, ok := value.(bool)
	if !ok {
		return fallback
	}
	return b
}

// StringOptionOr reads a string option, falling back when the Options map is
// nil, the key is absent, or the value is not a string.
func (r *Request) StringOptionOr(key string, fallback string) string {
	if r.Options == nil {
		return fallback
	}
	value, present := r.Options[key]
	if !present {
		return fallback
	}
	s, ok := value.(string)
	if !ok {
		return fallback
	}
	return s
}

// maxLoggedRequestLen bounds the request rendering that goes to the log file.
// The daemon logs every request it receives, and /var/log/tape.log has no
// rotation, so a client must not be able to write arbitrarily much through it.
const maxLoggedRequestLen = 512

func (r *Request) String() string {
	s := fmt.Sprintf("Request{Type: %v, Data: %v, Options: %v}", r.Type, r.Data, r.Options)
	if len(s) > maxLoggedRequestLen {
		return s[:maxLoggedRequestLen] + fmt.Sprintf("...(%d bytes truncated)}", len(s)-maxLoggedRequestLen)
	}
	return s
}
