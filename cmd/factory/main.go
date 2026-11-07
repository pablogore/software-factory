// Command factory is the thin client of the factoryd API: operator commands,
// the worker surface (factory worker …) and the read-only TUI (factory top).
package main

import (
	"context"
	"fmt"
	"os"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("factory", version)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "worker" {
		os.Exit(runWorker(context.Background(), os.Args[2:], os.Getenv, os.Stdin, os.Stdout, os.Stderr))
	}
	fmt.Fprintln(os.Stderr, "factory: unknown command; there are only version and worker; see docs/factoryd/README.md")
	os.Exit(2)
}
