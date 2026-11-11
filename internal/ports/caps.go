package ports

import (
	"context"

	"github.com/pablogore/software-factory/internal/domain"
)

// CapLog keeps, per issue, the caps a person raised and the stop it waits
// in. It lives in factoryd's store, so after a restart an issue stopped at a
// cap stays stopped under the same wait, and a raised cap stays raised.
type CapLog interface {
	// SaveIssueCaps saves c for issue c.Issue, replacing what was saved.
	SaveIssueCaps(ctx context.Context, c IssueCaps) error
	// IssueCaps is what SaveIssueCaps saved last for issue n; IssueCaps{Issue:
	// n}, nothing raised and not stopped, when it never ran.
	IssueCaps(ctx context.Context, n int) (IssueCaps, error)
}

// IssueCaps is one issue's state under the run's caps.
type IssueCaps struct {
	Issue int `json:"issue"`
	// Slices and USD are the caps a person raised for the issue; 0 when
	// the run's cap holds.
	Slices int     `json:"slices,omitempty"`
	USD    float64 `json:"usd,omitempty"`
	// Waits counts the issue's stops: the next one waits under Waits+1.
	Waits int `json:"waits,omitempty"`
	// Stop is the stop the issue waits in; nil when it is not stopped.
	Stop *CapStop `json:"stop,omitempty"`
}

// CapStop is the slice a stopped issue did not start, the cap it stopped at
// with the value it reached, and the wait a raise must answer.
type CapStop struct {
	// Next asked for the slice, and Job is the job it starts as.
	Next    domain.NextSliceRequested `json:"next"`
	Job     domain.JobID              `json:"job"`
	Cap     domain.IssueCap           `json:"cap"`
	Reached float64                   `json:"reached"`
	WaitSeq int                       `json:"wait_seq"`
}
