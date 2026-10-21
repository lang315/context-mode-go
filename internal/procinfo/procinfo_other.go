//go:build !darwin && !linux

package procinfo

import "errors"

// System has no process table on other systems; ClaudePID then returns 0.
func System() Lookup {
	return func(int) (int, string, error) { return 0, "", errors.New("procinfo: unsupported OS") }
}
