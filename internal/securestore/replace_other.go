//go:build !windows

package securestore

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
