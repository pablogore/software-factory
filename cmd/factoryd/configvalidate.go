package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/pablogore/software-factory/internal/wiring"
)

// configValidate loads every role and pipeline of a configuration root and
// prints what is worth a look without failing: a warning is not an error.
func configValidate(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("config-validate", flag.ContinueOnError)
	dir := fs.String("config", ".", "configuration root, with roles.yaml, roles/ and pipelines/")
	if err := fs.Parse(args); err != nil {
		return err
	}
	warnings, err := wiring.ValidateRoles(*dir)
	if err != nil {
		return err
	}
	for _, w := range warnings {
		_, _ = fmt.Fprintln(out, "warning:", w)
	}
	if len(warnings) == 0 {
		_, _ = fmt.Fprintf(out, "configuration in %s is valid\n", *dir)
		return nil
	}
	_, _ = fmt.Fprintf(out, "configuration in %s is valid, with warnings\n", *dir)
	return nil
}
