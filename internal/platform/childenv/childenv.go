// Package childenv is the environment of the processes factoryd runs on a
// job's code, its agent and its gate's checks: an allowlist of factoryd's
// own, so no credential factoryd holds reaches code a job's branch wrote.
package childenv

import (
	"os"
	"path/filepath"
	"strings"
)

// passed are the variables a job's process gets from factoryd's
// environment as they are; every LC_* variable is passed too.
var passed = []string{"PATH", "LANG", "TMPDIR"}

// Base is the environment of a process factoryd runs in the job's worktree
// home: PATH, the locale and TMPDIR from factoryd's environment, HOME set to
// home, and the Go caches where factoryd's own are.
func Base(home string) []string {
	var e []string
	for _, k := range passed {
		if v, ok := os.LookupEnv(k); ok {
			e = append(e, k+"="+v)
		}
	}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "LC_") {
			e = append(e, kv)
		}
	}
	e = append(e, "HOME="+home)
	return append(e, caches()...)
}

// caches are the Go caches factoryd uses, set even when factoryd's
// environment leaves them to their defaults: those hang off HOME, and a
// HOME in the worktree would leave the checks downloading and building
// everything again, into the worktree.
func caches() []string {
	var e []string
	if v, ok := os.LookupEnv("GOMODCACHE"); ok {
		e = append(e, "GOMODCACHE="+v)
	}
	if v, ok := os.LookupEnv("GOPATH"); ok {
		e = append(e, "GOPATH="+v)
	} else if h, err := os.UserHomeDir(); err == nil {
		e = append(e, "GOPATH="+filepath.Join(h, "go"))
	}
	cache, err := os.UserCacheDir()
	for _, c := range []struct{ name, dir string }{{"GOCACHE", "go-build"}, {"GOLANGCI_LINT_CACHE", "golangci-lint"}} {
		if v, ok := os.LookupEnv(c.name); ok {
			e = append(e, c.name+"="+v)
		} else if err == nil {
			e = append(e, c.name+"="+filepath.Join(cache, c.dir))
		}
	}
	return e
}
