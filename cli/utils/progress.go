package utils

import (
	"fmt"
	"os"
	"tape/common/global"
	"time"

	"github.com/schollz/progressbar/v3"
)

var progressBar *progressbar.ProgressBar

func ProgressNew(description string) {
	if global.IsVerbose() {
		return
	}
	progressBar = progressbar.NewOptions(100,
		progressbar.OptionSetDescription(description),
		progressbar.OptionSetPredictTime(true),
		progressbar.OptionSetWidth(10),
		progressbar.OptionThrottle(65*time.Millisecond),
		progressbar.OptionOnCompletion(func() {
			fmt.Fprint(os.Stderr, "\n")
		}),
		progressbar.OptionSpinnerType(14),
		progressbar.OptionFullWidth(),
		progressbar.OptionSetRenderBlankState(true),
	)

}

// ProgressSet advances the bar. It tolerates a Progress frame arriving without
// a preceding Start (which would leave progressBar nil) and a value that is not
// the int8 the daemon is supposed to send.
func ProgressSet(progress interface{}) {
	if global.IsVerbose() || progressBar == nil {
		return
	}

	value, ok := progress.(int8)
	if !ok {
		return
	}
	// The daemon derives this from Content-Length, which is -1 when the length
	// is unknown; clamp rather than feed a negative into the bar.
	if value < 0 {
		return
	}
	if value > 100 {
		value = 100
	}

	if err := progressBar.Set(int(value)); err != nil {
		return
	}
}
