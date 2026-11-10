// Package jobtoken issues the token a session's agent calls factoryd with:
// one per session, bound to one job, role and kind (01-diseno.md), and to
// the round and stage entry the job was in when it was issued. It denies by
// default: a job token authorizes only submitting its own job's result,
// reporting progress on it, running its checks for a build role that lists
// it, running its dev server for a builder or a UX or UI reviewer that
// lists it, asking a person a question for a spec or approve role that
// lists it and reading its context pack for a role that lists it, and of
// those only what its role's tools allow, so it never works
// for another job or for an operator's operation, even one added later, nor
// once the job moved on.
package jobtoken

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
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

// The operations a job token allows; every other one is refused.
const (
	OpSubmitResult Op = "worker.submit_result"
	OpProgress     Op = "worker.progress"
	OpRunGate      Op = "worker.run_gate"
	OpDevServer    Op = "worker.dev_server"
	OpAskHuman     Op = "worker.ask_human"
	OpContextPack  Op = "worker.context_pack"
)

// tools names the worker tool each operation is called by, as a role's
// `tools:` lists it.
var tools = map[Op]string{
	OpSubmitResult: "submit_result",
	OpProgress:     "progress",
	OpRunGate:      "run_gate",
	OpDevServer:    "dev_server",
	OpAskHuman:     "ask_human",
	OpContextPack:  "context_pack",
}

// Ops lists, sorted, every operation a token may allow: one per worker
// tool, named "worker." and the tool.
func Ops() []Op {
	return slices.Sorted(maps.Keys(tools))
}

// defaults are the tools a role that lists none may call: the ones every
// role had before roles listed tools. A tool added since is denied to it.
var defaults = []string{"submit_result", "progress"}

// kinds limits a tool to the roles of some kinds, even one that lists it:
// only a builder runs the job's checks, and only a spec-writer or an
// approver asks a person (01-diseno.md).
var kinds = map[Op][]domain.Kind{
	OpRunGate:  {domain.KindBuild},
	OpAskHuman: {domain.KindSpec, domain.KindApprove},
}

// roles limits a tool to the roles named, even when another lists it: only
// the builder and the UX and UI reviewers look at the running app (#37).
var roles = map[Op][]string{
	OpDevServer: {"builder", "reviewer-ux", "reviewer-ui"},
}

// Grant is what a token was issued for: the job and role of one session, the
// kind whose result that role submits, the tools the role may call (none
// means submit_result and progress), and the job's round and stage
// entry (Entry) when the session started.
type Grant struct {
	Job   domain.JobID
	Role  string
	Kind  domain.Kind
	Tools []string
	Round int
	Entry int
}

// For is the grant for a session of role, submitting its kind with its
// tools, on job as it is now: hist is job's history, which tells its stage
// entry.
func For(job domain.Job, hist []ports.Record, role domain.Role) Grant {
	return Grant{Job: job.ID, Role: role.Name, Kind: role.Kind, Tools: slices.Clone(role.Tools), Round: job.Round, Entry: Entry(hist)}
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
// OpSubmitResult, OpProgress, OpRunGate, OpDevServer, OpAskHuman or
// OpContextPack on the job it was issued for, only when the grant lists op's tool, or lists none
// and the tool is one of defaults, and only for a grant of a kind and of
// one of the roles the tool is limited to, if any.
// It decides on the call, not on the tools an agent was shown, so an agent
// that calls a tool its role may not use is refused with an error naming the
// tool and the role. The error never says which job the token belongs to.
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
	tool, ok := tools[op]
	if !ok {
		return Grant{}, fmt.Errorf("%w: operation %q", ErrForbidden, op)
	}
	if job != g.Job {
		return Grant{}, fmt.Errorf("%w: job %q", ErrForbidden, job)
	}
	allowed := g.Tools
	if len(allowed) == 0 {
		allowed = defaults
	}
	only, limited := kinds[op]
	named, byRole := roles[op]
	if !slices.Contains(allowed, tool) || (limited && !slices.Contains(only, g.Kind)) || (byRole && !slices.Contains(named, g.Role)) {
		return Grant{}, fmt.Errorf("%w: role %q may not use tool %q", ErrForbidden, g.Role, tool)
	}
	return g, nil
}
