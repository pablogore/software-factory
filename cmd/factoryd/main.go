// Command factoryd runs the software factory server.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("factoryd", version)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "worker-demo" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		err := workerDemo(ctx, os.Args[2:], os.Stdout)
		stop()
		if err != nil {
			fmt.Fprintln(os.Stderr, "factoryd worker-demo:", err)
			os.Exit(1)
		}
		return
	}
	fmt.Fprintln(os.Stderr, "factoryd: nothing to run yet; see docs/factoryd/README.md")
	os.Exit(2)
}
