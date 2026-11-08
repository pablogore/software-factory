// Command factoryd runs the software factory server.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/pablogore/software-factory/internal/adapters/out/acp/acpfake"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	// role-demo launches this binary again as its fake ACP agent.
	if acpfake.Requested() {
		os.Exit(acpfake.Run())
	}
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
	if len(os.Args) > 1 && os.Args[1] == "attempt-demo" {
		if err := attemptDemo(context.Background(), os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "factoryd attempt-demo:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "gate-demo" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		err := gateDemo(ctx, os.Args[2:], os.Stdout)
		stop()
		if err != nil {
			fmt.Fprintln(os.Stderr, "factoryd gate-demo:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "role-demo" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		err := roleDemo(ctx, os.Args[2:], os.Stdout)
		stop()
		if err != nil {
			fmt.Fprintln(os.Stderr, "factoryd role-demo:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "config-validate" {
		if err := configValidate(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "factoryd config-validate:", err)
			os.Exit(1)
		}
		return
	}
	fmt.Fprintln(os.Stderr, "factoryd: nothing to run yet; see docs/factoryd/README.md")
	os.Exit(2)
}
