package utils

import (
	"math"
	"strconv"
)

// Binary (1024-based) suffixes. Immutable package state: this used to be a
// mutable array rewritten on every call, which raced across the daemon's
// per-connection goroutines.
var byteSuffixes = [...]string{"B", "KB", "MB", "GB", "TB"}

func round(val float64, roundOn float64, places int) (newVal float64) {
	var round float64
	pow := math.Pow(10, float64(places))
	digit := pow * val
	_, div := math.Modf(digit)
	if div >= roundOn {
		round = math.Ceil(digit)
	} else {
		round = math.Floor(digit)
	}
	newVal = round / pow
	return
}

// ConvertBytesToHumanReadable renders a byte count using binary (1024) units.
//
// A negative size means "length unknown": net/http reports ContentLength as -1
// for chunked and transparently-decompressed responses, and that value reaches
// this function directly from the daemon's download path.
func ConvertBytesToHumanReadable(size int64) string {
	if size < 0 {
		return "unknown"
	}
	if size < 1024 {
		return strconv.FormatInt(size, 10) + " " + byteSuffixes[0]
	}

	value := float64(size)
	exp := 0
	// Stop at the largest suffix we have rather than indexing past the table.
	for value >= 1024 && exp < len(byteSuffixes)-1 {
		value /= 1024
		exp++
	}

	return strconv.FormatFloat(round(value, .5, 2), 'f', -1, 64) + " " + byteSuffixes[exp]
}
