package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"
)

// serveSection is the heading the serve options block of 05-config.md
// starts with.
const serveSection = "**Opciones de `factoryd serve`"

// serveUsage is what `factoryd serve -h` prints. The flag package writes
// it to os.Stderr, so it is swapped for a pipe while serve runs.
func serveUsage(ctx *specs.Context) string {
	r, w, err := os.Pipe()
	ctx.Expect(err).To(specs.BeNil())
	stderr := os.Stderr
	os.Stderr = w
	read := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		read <- string(b)
	}()
	err = serve(context.Background(), []string{"-h"}, io.Discard)
	os.Stderr = stderr
	ctx.Expect(w.Close()).To(specs.BeNil())
	ctx.Expect(errors.Is(err, flag.ErrHelp)).To(specs.BeTrue())
	return <-read
}

// serveDefaults maps each flag in usage to its default as the flag package
// prints it, empty for zero values, which it leaves out.
func serveDefaults(usage string) map[string]string {
	defaults := map[string]string{}
	name := ""
	for _, line := range strings.Split(usage, "\n") {
		if rest, ok := strings.CutPrefix(line, "  -"); ok {
			name, _, _ = strings.Cut(rest, " ")
			defaults[name] = ""
			continue
		}
		if _, def, ok := strings.Cut(line, "(default "); ok && name != "" {
			defaults[name] = strings.TrimSuffix(def, ")")
		}
	}
	return defaults
}

// documented is how def reads in the docs: unquoted, durations without
// their zero minutes and seconds, and a path in the user's cache relative
// to it.
func documented(def string) string {
	if s, err := strconv.Unquote(def); err == nil {
		def = s
	}
	if d, err := time.ParseDuration(def); err == nil {
		def = d.String()
		if strings.HasSuffix(def, "m0s") {
			def = strings.TrimSuffix(def, "0s")
		}
		if strings.HasSuffix(def, "h0m") {
			def = strings.TrimSuffix(def, "0m")
		}
	}
	if cache, err := os.UserCacheDir(); err == nil {
		if rel, err := filepath.Rel(cache, def); err == nil && filepath.IsAbs(def) && !strings.HasPrefix(rel, "..") {
			def = filepath.ToSlash(rel)
		}
	}
	return def
}

// serveDocs is the serve options block of 05-config.md, up to the next
// heading.
func serveDocs(ctx *specs.Context) string {
	b, err := os.ReadFile("../../docs/factoryd/05-config.md")
	ctx.Expect(err).To(specs.BeNil())
	_, section, ok := strings.Cut(string(b), serveSection)
	ctx.Expect(ok).To(specs.BeTrue())
	section, _, _ = strings.Cut(section, "\n## ")
	return section
}

// flagLine is the bullet of section that documents name, empty if none.
func flagLine(section, name string) string {
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(line, "- `--"+name+"`") || strings.HasPrefix(line, "- `--"+name+" ") {
			return line
		}
	}
	return ""
}

func TestServeFlagsDocumented(t *testing.T) {
	specs.Describe(t, "factoryd serve's options in 05-config.md", func(s *specs.Spec) {
		s.It("documents every flag of serve with its default", func(ctx *specs.Context) {
			defaults := serveDefaults(serveUsage(ctx))
			ctx.Expect(len(defaults) > 0).To(specs.BeTrue())
			section := serveDocs(ctx)
			for name, def := range defaults {
				line := flagLine(section, name)
				if line == "" {
					ctx.Testing().Errorf("--%s is a flag of serve but 05-config.md does not document it", name)
					continue
				}
				if def == "" {
					continue
				}
				if want := "`" + documented(def) + "`"; !strings.Contains(line, want) {
					ctx.Testing().Errorf("--%s defaults to %s but 05-config.md does not say %s: %s", name, def, want, line)
				}
			}
		})
		s.It("says --store is required and --budget must be above 0", func(ctx *specs.Context) {
			section := serveDocs(ctx)
			ctx.Expect(flagLine(section, "store")).To(specs.Contain("obligatorio"))
			ctx.Expect(flagLine(section, "budget")).To(specs.Contain("mayor que 0"))
		})
		s.It("keeps the warnings of --listen and --costs", func(ctx *specs.Context) {
			section := serveDocs(ctx)
			ctx.Expect(flagLine(section, "listen")).To(specs.Contain("loopback"))
			ctx.Expect(flagLine(section, "costs")).To(specs.Contain("`subscription` cuenta 0 USD"))
		})
	})
}
