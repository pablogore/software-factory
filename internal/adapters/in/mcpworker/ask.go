package mcpworker

import (
	"fmt"

	"github.com/pablogore/software-factory/internal/domain"
)

// ToolAskHuman is the MCP tool a spec-writer or an approver asks a person a
// question with.
const ToolAskHuman = "ask_human"

// askSchema is the ask_human tool's arguments, the same JSON object POST
// AskPath takes as its body. The Submitter decodes and checks them, so a
// blank question or an unknown audience is refused as a tool error.
var askSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"question": map[string]any{
			"type":        "string",
			"description": fmt.Sprintf("the question for the person, in at most %d characters", domain.MaxQuestionText),
		},
		"options": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": fmt.Sprintf("the answers the person may choose from, at most %d of at most %d characters each; none for an open question", domain.MaxOptions, domain.MaxOptionText),
		},
		"to": map[string]any{
			"type":        "string",
			"enum":        []any{string(domain.AudienceProduct), string(domain.AudienceTech)},
			"description": "who should answer: the product person or the technical one",
		},
	},
	"required":             []any{"question", "to"},
	"additionalProperties": false,
}
