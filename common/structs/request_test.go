package structs

import (
	"tape/common/enums"
	"testing"
)

// Every one of these shapes used to panic the root daemon, because the
// handlers reached for request.Data.(T) and request.Options[k].(bool) with no
// comma-ok. A nil Options map was the cheapest kill: indexing it yields a nil
// interface, and asserting bool on nil panics.

func TestStringDataRejectsWrongTypes(t *testing.T) {
	cases := []struct {
		name string
		req  Request
	}{
		{"nil data", Request{Type: enums.RequestTypeQueryPkg}},
		{"int data", Request{Type: enums.RequestTypeQueryPkg, Data: 42}},
		{"map data", Request{Type: enums.RequestTypeQueryPkg, Data: map[string]string{"a": "b"}}},
		{"slice data", Request{Type: enums.RequestTypeQueryPkg, Data: []string{"a"}}},
		{"bool data", Request{Type: enums.RequestTypeQueryPkg, Data: true}},
		{"empty string", Request{Type: enums.RequestTypeQueryPkg, Data: ""}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.req.StringData(); err == nil {
				t.Error("StringData() = nil error, want error")
			}
		})
	}
}

func TestStringDataAcceptsString(t *testing.T) {
	req := Request{Type: enums.RequestTypeQueryPkg, Data: "bash"}
	got, err := req.StringData()
	if err != nil {
		t.Fatalf("StringData() error = %v", err)
	}
	if got != "bash" {
		t.Errorf("StringData() = %q, want %q", got, "bash")
	}
}

func TestStringMapDataRejectsWrongTypes(t *testing.T) {
	cases := []struct {
		name string
		req  Request
	}{
		{"nil data", Request{Type: enums.RequestTypeDownloadPkg}},
		{"string data", Request{Type: enums.RequestTypeDownloadPkg, Data: "not a map"}},
		{"int data", Request{Type: enums.RequestTypeDownloadPkg, Data: 7}},
		{"wrong map type", Request{Type: enums.RequestTypeDownloadPkg, Data: map[string]int{"a": 1}}},
		{"empty map", Request{Type: enums.RequestTypeDownloadPkg, Data: map[string]string{}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.req.StringMapData(); err == nil {
				t.Error("StringMapData() = nil error, want error")
			}
		})
	}
}

func TestStringMapDataAcceptsMap(t *testing.T) {
	req := Request{
		Type: enums.RequestTypeDownloadPkg,
		Data: map[string]string{"repo": "core", "name": "bash"},
	}
	got, err := req.StringMapData()
	if err != nil {
		t.Fatalf("StringMapData() error = %v", err)
	}
	if got["name"] != "bash" {
		t.Errorf("got[name] = %q, want %q", got["name"], "bash")
	}
}

// The five-line daemon killer: a request with no Options at all.
func TestBoolOptionOrSurvivesNilOptions(t *testing.T) {
	req := Request{Type: enums.RequestTypeRefreshRepos}

	if got := req.BoolOptionOr("force", false); got != false {
		t.Errorf("BoolOptionOr on nil Options = %v, want false", got)
	}
	if got := req.BoolOptionOr("force", true); got != true {
		t.Errorf("BoolOptionOr fallback not honoured, got %v want true", got)
	}
}

func TestBoolOptionOrIgnoresWrongType(t *testing.T) {
	req := Request{
		Type:    enums.RequestTypeRefreshRepos,
		Options: map[string]interface{}{"force": "yes please"},
	}

	if got := req.BoolOptionOr("force", false); got != false {
		t.Errorf("BoolOptionOr with non-bool value = %v, want fallback false", got)
	}
}

func TestBoolOptionOrReadsRealValue(t *testing.T) {
	req := Request{
		Type:    enums.RequestTypeRefreshRepos,
		Options: map[string]interface{}{"force": true},
	}

	if got := req.BoolOptionOr("force", false); got != true {
		t.Errorf("BoolOptionOr = %v, want true", got)
	}
}

// String() is called on every request for the log line, including hostile ones.
func TestRequestStringIsBounded(t *testing.T) {
	huge := make([]byte, 0, 8192)
	for i := 0; i < 8192; i++ {
		huge = append(huge, 'A')
	}

	req := Request{Type: enums.RequestTypeQueryPkg, Data: string(huge)}
	got := req.String()

	if len(got) > 1024 {
		t.Errorf("Request.String() produced %d bytes; unbounded request data reaches the log verbatim", len(got))
	}
}
