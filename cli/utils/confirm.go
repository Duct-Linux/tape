package utils

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Confirm asks a yes/no question on stderr.
//
// A non-interactive stdin (`tape install foo </dev/null`, or any script) used
// to spin this at full CPU forever: the read error was discarded and an empty
// string never satisfied the loop condition. EOF now answers "no", which is the
// safe default for a prompt that gates installing software.
func Confirm(msg string) bool {
	r := bufio.NewReader(os.Stdin)

	for {
		fmt.Fprintf(os.Stderr, "%s [y/N]: ", msg)

		line, err := r.ReadString('\n')
		answer := strings.ToLower(strings.TrimSpace(line))

		if err != nil {
			// EOF with a trailing answer still counts; EOF with nothing does not.
			if answer == "y" || answer == "yes" {
				return true
			}
			fmt.Fprintln(os.Stderr)
			return false
		}

		switch answer {
		case "y", "yes":
			return true
		case "n", "no", "":
			return false
		}
		// Anything else: ask again.
	}
}
