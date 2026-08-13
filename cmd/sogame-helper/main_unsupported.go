//go:build !windows || !amd64

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "sogame-helper is only supported on Windows amd64")
	os.Exit(1)
}
