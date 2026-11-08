package configyaml

import (
	"fmt"
	"strings"
)

type devServerFile struct {
	Command string `yaml:"command"`
}

// DevServer is what a repo's dev server file (.factory/devserver.yaml)
// configures for the worker's dev_server.
type DevServer struct {
	// Command starts the app's dev server on the port in $PORT, split on
	// spaces and run without a shell; none when the file sets none.
	Command []string
}

// LoadDevServer parses a repo's dev server file. An unknown key is an
// error, so a misspelt command is never taken for no command.
func LoadDevServer(data []byte) (DevServer, error) {
	var f devServerFile
	if err := decodeStrict(data, &f); err != nil {
		return DevServer{}, fmt.Errorf("parse dev server: %w", err)
	}
	return DevServer{Command: strings.Fields(f.Command)}, nil
}
