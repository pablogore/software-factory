package configyaml

import (
	"fmt"
	"strings"
)

type gatesFile struct {
	CountCommand string `yaml:"count_command"`
}

// Gates is what a repo's gates file (.factory/gates.yaml) configures for
// the gate.
type Gates struct {
	// CountCommand is the test count command, split on spaces and run
	// without a shell, as gate-demo -count takes it; none when the file
	// sets none.
	CountCommand []string
}

// LoadGates parses a repo's gates file. An unknown key is an error, so a
// misspelt count_command never turns the rule off unnoticed.
func LoadGates(data []byte) (Gates, error) {
	var f gatesFile
	if err := decodeStrict(data, &f); err != nil {
		return Gates{}, fmt.Errorf("parse gates: %w", err)
	}
	return Gates{CountCommand: strings.Fields(f.CountCommand)}, nil
}
