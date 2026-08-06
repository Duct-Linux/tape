package wrapper

import "fmt"

func errNotAStringSlice(v interface{}) error {
	return fmt.Errorf("request data must be a []string, got %T", v)
}

func errQueryTooLong() error {
	return fmt.Errorf("search query is too long")
}
