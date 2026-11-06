# AGENTS.md: factoryd

Read this before working on this branch. It applies to every agent (spec-writer, builder, reviewers, approver) and to humans.

## What this is
factoryd: the software factory rewritten as a Go server, agent-agnostic (Claude Code, Codex, Gemini, OpenCode, Pi…). Go decides everything deterministic (state, queue, gates, permissions, integrations); agents only do work that needs judgment.

The design lives in `docs/factoryd/` (read `README.md` there first; numbered docs, read in order; a higher number wins on conflict). The backlog is in GitHub issues; each one names the docs it implements.

## Build and check
- Go 1.27+, module `github.com/pablogore/software-factory` (required by go-specs).
- `make check` runs gofmt, vet, tests, golangci-lint (if installed) and the build. The factory's gate runs exactly this; it must pass before review.
- The server and the CLI build with `CGO_ENABLED=0`. Only `cmd/factory-desktop` (Wails) may use cgo, behind its own build.

## Architecture rules (enforced by `internal/arch_test.go`)
Hexagonal + vertical slices. See `docs/factoryd/07-arquitectura.md`.

| Package | May import | Must not import |
|---|---|---|
| `internal/domain/...` | standard library only | anything else |
| `internal/ports` | `domain` | slices, adapters, platform |
| `internal/platform/...` | `domain`, `ports` | slices, adapters |
| `internal/slices/<name>/...` | `domain`, `ports`, `platform` | other slices, adapters |
| `internal/adapters/...` | `domain`, `ports`, `platform`, external libs | slices |
| `internal/wiring` | everything | — |
| `ui/...` | `pkg/...` (API client and types) | `internal/...` |
| `pkg/...` | `pkg/...` | `internal/...` |

- Slices never call each other: they publish and consume domain events.
- Every external side effect (GitHub, chats, push, PR) goes through the outbox port.
- Interfaces are declared by the consumer: a slice declares in its own `ports.go` only the methods it uses.
- `decide.Decide` is pure: no IO, no `time.Now()`; time comes from a `Clock` port.

## Code conventions
- Standard library first. Approved dependencies, usable without escalating:
  - `github.com/getsyntegrity/go-specs` (tests only, pinned);
  - `go.yaml.in/yaml/v3` (only in config adapters such as `internal/adapters/out/configyaml`);
  - `modernc.org/sqlite` (only in `internal/adapters/out/sqlite`; pure Go, no cgo).

  Any other new dependency must be justified in the PR description and goes to a person; prefer pure-Go (no cgo) libraries.
- Errors: wrap with `%w`, compare with `errors.Is/As`. No panics outside `main`.
- `context.Context` is the first parameter of anything that does IO or may block.
- Unit tests use **go-specs** (`github.com/getsyntegrity/go-specs`), pinned to an exact version (it is pre-1.0: never upgrade it in a feature PR). Do not add testify, gomega or other assertion libraries.
  - Structure: `specs.Describe(t, "<unit>", func(s *specs.Spec) { s.When(...); s.It(...) })`, assertions with `ctx.Expect(x).ToEqual(y)` and its matchers.
  - Case tables: `specs.Table(s, rows, nameFn, body)` (it validates row names; use it for `decide` transitions and parity cases ported from `main`).
  - Adapter contract suites: a shared behavior, i.e. a plain `func(s *specs.Spec, newX func() ports.X)` called from the `Describe` of each adapter and of the in-memory fake.
  - Fakes in memory first; `mock.Controller` and spies only to assert interactions (call order, counts) that a fake can't show.
  - Invariants of `decide` (rounds never exceed the maximum, no transition out of a stopped job, same input → same output): native Go fuzz tests (`FuzzXxx`). `go-specs/property` is a separate module without a tagged release yet: don't add it until it has one.
  - Stable outputs (CLI `--json`, `context_pack`, prompts sent to agents): snapshot tests with `go-specs/snapshots`.
  - Never commit `s.FIt` (it fails the suite) and never use `SkipIt`/`PendingIt` to make the gate pass.
- Domain and slice tests never touch the network, real agents or GitHub.
- Small packages, small files; comments explain why, not what.
- Identifiers, code comments and commit messages in English. User-facing docs in `docs/factoryd/` are in Spanish.

## Out of bounds for the factory's agents
- Do not modify `docs/factoryd/0*.md` (the design) unless the issue says so; propose changes in the PR description instead.
- Do not modify `internal/arch_test.go`, `Makefile`, `.github/workflows/` or `AGENTS.md` unless the issue is about them.
- Never weaken, skip or delete a test to make the gate pass.

## Legacy reference
The current factory (Python scripts, Claude Code skill and agents) lives on `main`: `factory/bin/job`, `factory/bin/gate`, `factory/bin/specgate`, `factory/bin/route`, `factory/tests/`, `.claude/agents/`. Read it with `git show origin/main:<path>` when an issue asks for parity with it.
