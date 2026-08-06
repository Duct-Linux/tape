package utils

import "testing"

func TestConvertBytesToHumanReadable(t *testing.T) {
	tests := []struct {
		name string
		size int64
		want string
	}{
		// The cases that crash today. A zero-length repo.db yields size 0, and
		// resp.ContentLength is -1 for any chunked or transparently-gzipped
		// HTTP response -- both reach this function straight from the daemon.
		{"zero", 0, "0 B"},
		{"unknown length", -1, "unknown"},
		{"negative", -4096, "unknown"},

		{"one byte", 1, "1 B"},
		{"just under a KB", 1023, "1023 B"},
		{"exactly a KB", 1024, "1 KB"},
		{"a MB", 1024 * 1024, "1 MB"},
		{"a GB", 1024 * 1024 * 1024, "1 GB"},
		{"a TB", 1024 * 1024 * 1024 * 1024, "1 TB"},
		// Must not run off the end of the suffix table.
		{"a PB", 1024 * 1024 * 1024 * 1024 * 1024, "1024 TB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConvertBytesToHumanReadable(tt.size)
			if got != tt.want {
				t.Errorf("ConvertBytesToHumanReadable(%d) = %q, want %q", tt.size, got, tt.want)
			}
		})
	}
}

// The suffix table used to be a package-level array rewritten on every call,
// which raced across daemon connection goroutines.
func TestConvertBytesToHumanReadableIsConcurrencySafe(t *testing.T) {
	done := make(chan string, 64)
	for i := 0; i < 64; i++ {
		go func() { done <- ConvertBytesToHumanReadable(4096) }()
	}
	for i := 0; i < 64; i++ {
		if got := <-done; got != "4 KB" {
			t.Fatalf("concurrent call returned %q, want %q", got, "4 KB")
		}
	}
}
