// Command factoryd runs the software factory server.
package main

import (
	"fmt"
	"os"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("factoryd", version)
		return
	}
	fmt.Fprintln(os.Stderr, "factoryd: nothing to run yet; see docs/factoryd/README.md")
	os.Exit(2)
}
