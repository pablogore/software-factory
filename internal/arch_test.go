package internal_test

// This test enforces the dependency rules of docs/factoryd/07-arquitectura.md.
// It runs with `go test ./...`, so the factory's gate rejects a forbidden import.

import (
	"bufio"
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

const module = "github.com/pablogore/software-factory"

// layer returns the architectural layer of an import path inside this module,
// and for slices the slice name.
func layer(path string) (name, slice string) {
	rel := strings.TrimPrefix(path, module+"/")
	switch {
	case rel == path:
		return "external", ""
	case strings.HasPrefix(rel, "internal/domain"):
		return "domain", ""
	case strings.HasPrefix(rel, "internal/ports"):
		return "ports", ""
	case rel == "internal/slices":
		return "slices-root", ""
	case strings.HasPrefix(rel, "internal/slices/"):
		return "slice", strings.SplitN(strings.TrimPrefix(rel, "internal/slices/"), "/", 2)[0]
	case strings.HasPrefix(rel, "internal/platform"):
		return "platform", ""
	case strings.HasPrefix(rel, "internal/adapters"):
		return "adapter", ""
	case strings.HasPrefix(rel, "internal/wiring"):
		return "wiring", ""
	case strings.HasPrefix(rel, "cmd/"):
		return "cmd", ""
	case strings.HasPrefix(rel, "ui/"):
		return "ui", ""
	case strings.HasPrefix(rel, "pkg/"):
		return "public", "" // API client and its types, shared by the CLI and the UIs
	default:
		return "other", ""
	}
}

// allowed says whether a package in layer `from` may import a package in layer `to`.
func allowed(from, fromSlice, to, toSlice string) bool {
	if to == "external" {
		return from != "domain" // domain uses only the standard library
	}
	switch from {
	case "domain":
		return to == "domain"
	case "ports":
		return to == "domain" || to == "ports"
	case "platform":
		return to == "domain" || to == "ports" || to == "platform"
	case "slice":
		if to == "slice" {
			return toSlice == fromSlice
		}
		return to == "domain" || to == "ports" || to == "platform"
	case "adapter":
		return to == "domain" || to == "ports" || to == "platform" || to == "adapter"
	case "ui":
		return to == "ui" || to == "public"
	case "public":
		return to == "public"
	default: // wiring, cmd, slices-root, other
		return true
	}
}

func isStdlib(path string) bool {
	first := strings.SplitN(path, "/", 2)[0]
	return !strings.Contains(first, ".")
}

func TestDependencyRules(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", `{{.ImportPath}} {{join .Imports " "}}`, module+"/...").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 {
			continue
		}
		pkg := fields[0]
		from, fromSlice := layer(pkg)
		for _, imp := range fields[1:] {
			if isStdlib(imp) {
				continue
			}
			to, toSlice := layer(imp)
			if !allowed(from, fromSlice, to, toSlice) {
				t.Errorf("%s (%s) must not import %s (%s); see docs/factoryd/07-arquitectura.md", pkg, from, imp, to)
			}
		}
	}
}

func TestRulesTable(t *testing.T) {
	cases := []struct {
		from, fromSlice, to, toSlice string
		ok                           bool
	}{
		{"domain", "", "domain", "", true},
		{"domain", "", "ports", "", false},
		{"domain", "", "external", "", false},
		{"ports", "", "domain", "", true},
		{"ports", "", "adapter", "", false},
		{"slice", "review", "slice", "review", true},
		{"slice", "review", "slice", "approve", false},
		{"slice", "review", "adapter", "", false},
		{"slice", "review", "platform", "", true},
		{"adapter", "", "slice", "approve", false},
		{"adapter", "", "external", "", true},
		{"platform", "", "slice", "x", false},
		{"wiring", "", "slice", "x", true},
		{"ui", "", "domain", "", false},
		{"ui", "", "public", "", true},
		{"public", "", "domain", "", false},
	}
	for _, c := range cases {
		if got := allowed(c.from, c.fromSlice, c.to, c.toSlice); got != c.ok {
			t.Errorf("allowed(%s/%s → %s/%s) = %v, want %v", c.from, c.fromSlice, c.to, c.toSlice, got, c.ok)
		}
	}
}
