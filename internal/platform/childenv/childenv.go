// Package childenv is the environment of the processes factoryd runs on a
// job's code, its agent and its gate's checks: an allowlist of factoryd's
// own, so no credential factoryd holds reaches code a job's branch wrote.
package childenv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pablogore/software-factory/internal/domain"
)

// passed are the variables a job's process gets from factoryd's
// environment as they are; every LC_* variable is passed too.
var passed = []string{"PATH", "LANG", "TMPDIR"}

// Base is the environment of a process factoryd runs on a job's code with
// home as its HOME: PATH, the locale and TMPDIR from factoryd's environment, HOME set to
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
// job's own HOME would leave the checks downloading and building everything
// again, for every job.
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

// Homes are the HOMEs of jobs' processes: one directory per job under Root,
// apart from every worktree, so a job's agent and gate share one and no two
// jobs do.
type Homes struct {
	Root string
}

// DefaultHomes keeps the jobs' HOMEs in factoryd's data directory, under
// the user's cache directory.
func DefaultHomes() (Homes, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return Homes{}, fmt.Errorf("childenv: homes: %w", err)
	}
	return Homes{Root: filepath.Join(dir, "factoryd", "homes")}, nil
}

// For is job's HOME, an absolute path, made when it is missing. It refuses
// ids that would land outside Root.
func (h Homes) For(job domain.JobID) (string, error) {
	if h.Root == "" {
		return "", fmt.Errorf("childenv: %w: homes root", domain.ErrMissingField)
	}
	id := string(job)
	if id == "" {
		return "", fmt.Errorf("childenv: home: %w: job id", domain.ErrMissingField)
	}
	if id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return "", fmt.Errorf("childenv: home of job %q: %w", id, domain.ErrInvalidValue)
	}
	home, err := filepath.Abs(filepath.Join(h.Root, id))
	if err != nil {
		return "", fmt.Errorf("childenv: home of job %q: %w", id, err)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", fmt.Errorf("childenv: home of job %q: %w", id, err)
	}
	return home, nil
}
