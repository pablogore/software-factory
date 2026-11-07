// Package jobtoken issues the token a session's agent calls factoryd with:
// one per session, bound to one job, role and kind (01-diseno.md). It denies
// by default: a job token authorizes only submitting its own job's result,
// so it never works for another job or for an operator's operation, even one
// added later.
package jobtoken

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"github.com/pablogore/software-factory/internal/domain"
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
)

// Op names what a caller asks to do with a token.
type Op string

// OpSubmitResult is the only operation a job token allows.
const OpSubmitResult Op = "worker.submit_result"

// Grant is what a token was issued for: the job and role of one session, and
// the kind whose result that role submits.
type Grant struct {
	Job  domain.JobID
	Role string
	Kind domain.Kind
}

// Validate refuses a grant without a job, with a bad role name or an
// unknown kind.
func (g Grant) Validate() error {
	if g.Job == "" {
		return fmt.Errorf("%w: job", domain.ErrMissingField)
	}
	if err := domain.ValidateRoleName(g.Role); err != nil {
		return err
	}
	return g.Kind.Validate()
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
