// Package jobtoken issues the token a session's agent calls factoryd with:
// one per session, bound to one job, role and kind (01-diseno.md), and to
// the round and stage entry the job was in when it was issued. It denies by
// default: a job token authorizes only submitting its own job's result, so
// it never works for another job or for an operator's operation, even one
// added later, nor once the job moved on.
package jobtoken

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/domain/decide"
	"github.com/pablogore/software-factory/internal/ports"
)

// Token errors. Callers compare with errors.Is.
var (
	// ErrNoToken means the call came without a token.
	ErrNoToken = errors.New("no job token")
	// ErrUnknownToken means the token was never issued or was revoked.
	ErrUnknownToken = errors.New("unknown job token")
	// ErrForbidden means the token is valid but not for this operation or
	// this job.
	ErrForbidden = errors.New("job token not allowed")
	// ErrStale means the token is for this job, but the job left the round
	// or stage entry it was issued in, even if it came back to that stage.
	ErrStale = errors.New("job token from an earlier round")
)

// Op names what a caller asks to do with a token.
type Op string

// OpSubmitResult is the only operation a job token allows.
const OpSubmitResult Op = "worker.submit_result"

// Grant is what a token was issued for: the job and role of one session, the
// kind whose result that role submits, and the job's round and stage entry
// (Entry) when the session started.
type Grant struct {
	Job   domain.JobID
	Role  string
	Kind  domain.Kind
	Round int
	Entry int
}

// For is the grant for a session of role, submitting kind, on job as it is
// now: hist is job's history, which tells its stage entry.
func For(job domain.Job, hist []ports.Record, role string, kind domain.Kind) Grant {
	return Grant{Job: job.ID, Role: role, Kind: kind, Round: job.Round, Entry: Entry(hist)}
}

// Entry numbers a job's entries into a stage from its history: it grows on
// every event that moves the job to a stage, the one it was in included, and
// never goes back, so a job that returns to a stage is in a new entry.
func Entry(hist []ports.Record) int {
	var job domain.Job
	n := 0
	for _, r := range hist {
		stage := job.Stage
		job = decide.Apply(job, []domain.Event{r.Event})
		if _, entered := r.Event.(domain.StageEntered); entered || job.Stage != stage {
			n++
		}
	}
	return n
}

// Validate refuses a grant without a job, with a bad role name, an unknown
// kind, no round or a negative entry.
func (g Grant) Validate() error {
	if g.Job == "" {
		return fmt.Errorf("%w: job", domain.ErrMissingField)
	}
	if err := domain.ValidateRoleName(g.Role); err != nil {
		return err
	}
	if err := g.Kind.Validate(); err != nil {
		return err
	}
	if g.Round < 1 {
		return fmt.Errorf("%w: round %d", domain.ErrInvalidValue, g.Round)
	}
	if g.Entry < 0 {
		return fmt.Errorf("%w: entry %d", domain.ErrInvalidValue, g.Entry)
	}
	return nil
}

// Current refuses with ErrStale unless job, with history hist, is still in
// the round and stage entry g was issued in.
func (g Grant) Current(job domain.Job, hist []ports.Record) error {
	if entry := Entry(hist); job.Round != g.Round || entry != g.Entry {
		return fmt.Errorf("%w: issued in round %d, stage entry %d; job %q is in round %d, stage entry %d",
			ErrStale, g.Round, g.Entry, job.ID, job.Round, entry)
	}
	return nil
}

// Tokens keeps the grants of the tokens it issued. Only a token's hash is
// kept, so looking one up never compares secrets byte by byte.
type Tokens struct {
	mu     sync.Mutex
	grants map[[sha256.Size]byte]Grant
}

// New returns a registry with no tokens.
func New() *Tokens {
	return &Tokens{grants: map[[sha256.Size]byte]Grant{}}
}

// Issue returns a new random token for g.
func (t *Tokens) Issue(g Grant) (string, error) {
	if err := g.Validate(); err != nil {
		return "", fmt.Errorf("jobtoken: issue: %w", err)
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("jobtoken: issue: %w", err)
	}
	token := hex.EncodeToString(b)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.grants[sha256.Sum256([]byte(token))] = g
	return token, nil
}

// Revoke forgets token, as when its session ends. An unknown token is a
// no-op.
func (t *Tokens) Revoke(token string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.grants, sha256.Sum256([]byte(token)))
}

// Authorize returns token's grant when it allows op on job: only
// OpSubmitResult on the job it was issued for. The error never says which
// job the token belongs to.
func (t *Tokens) Authorize(token string, op Op, job domain.JobID) (Grant, error) {
	if token == "" {
		return Grant{}, ErrNoToken
	}
	t.mu.Lock()
	g, ok := t.grants[sha256.Sum256([]byte(token))]
	t.mu.Unlock()
	if !ok {
		return Grant{}, ErrUnknownToken
	}
	if op != OpSubmitResult {
		return Grant{}, fmt.Errorf("%w: operation %q", ErrForbidden, op)
	}
	if job != g.Job {
		return Grant{}, fmt.Errorf("%w: job %q", ErrForbidden, job)
	}
	return g, nil
}
