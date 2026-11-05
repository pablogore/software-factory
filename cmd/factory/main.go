// Command factory is the thin client of the factoryd API: operator commands,
// the worker surface (factory worker …) and the read-only TUI (factory top).
package main

import (
	"fmt"
	"os"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("factory", version)
		return
	}
	fmt.Fprintln(os.Stderr, "factory: no commands yet; see docs/factoryd/README.md")
	os.Exit(2)
}
