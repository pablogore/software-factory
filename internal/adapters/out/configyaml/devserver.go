package configyaml

import (
	"fmt"
	"strings"

	"github.com/pablogore/software-factory/internal/domain"
)

type devServerFile struct {
	Command string            `yaml:"command"`
	Env     map[string]string `yaml:"env"`
}

// DevServer is what a repo's dev server file (.factory/devserver.yaml)
// configures for the worker's dev_server.
type DevServer struct {
	// Command starts the app's dev server on the port in $PORT, split on
	// spaces and run without a shell; none when the file sets none.
	Command []string
	// Env is the variables the command runs with, besides PATH, HOME,
	// LANG, TMPDIR and PORT: nothing else of factoryd's environment
	// reaches it.
	Env map[string]string
}

// LoadDevServer parses a repo's dev server file. An unknown key is an
// error, so a misspelt command is never taken for no command.
func LoadDevServer(data []byte) (DevServer, error) {
	var f devServerFile
	if err := decodeStrict(data, &f); err != nil {
		return DevServer{}, fmt.Errorf("parse dev server: %w", err)
	}
	for k := range f.Env {
		if k == "" || k == "PORT" || strings.ContainsAny(k, "= \x00") {
			return DevServer{}, fmt.Errorf("parse dev server: %w: env %q", domain.ErrInvalidValue, k)
		}
	}
	return DevServer{Command: strings.Fields(f.Command), Env: f.Env}, nil
}
