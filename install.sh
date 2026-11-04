#!/usr/bin/env bash
# Install the software factory (v3) into a project.
#
#   ./install.sh <project-path> [--base <branch>] [--force] [--no-labels] [--no-tests] [--no-checks] [--dry-run]
#
#   --base <branch>  branch the factory builds from and merges into (default: origin's default branch;
#                    use `develop` for Git Flow)
#   --force          overwrite factory files that already exist (the old ones go to .factory-backup-<time>/)
#   --no-labels      don't create the GitHub labels
#   --no-tests       don't run the factory's own tests after installing
#   --no-checks      don't run the project's factory/checks.sh while verifying
#   --dry-run        show what would change, change nothing
#
# Never overwrites the project's factory/config.json or factory/checks.sh (it adds missing config keys),
# never commits and never pushes.
set -euo pipefail

SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TARGET="" BASE="" FORCE=0 LABELS=1 TESTS=1 CHECKS=1 DRY=0
VERIFY_TIMEOUT="${FACTORY_VERIFY_TIMEOUT:-300}"

usage() { sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'; exit "${1:-0}"; }
while [ $# -gt 0 ]; do
  case "$1" in
    --base) BASE="${2:?--base needs a branch}"; shift 2 ;;
    --force) FORCE=1; shift ;;
    --no-labels) LABELS=0; shift ;;
    --no-tests) TESTS=0; shift ;;
    --no-checks) CHECKS=0; shift ;;
    --dry-run) DRY=1; shift ;;
    -h|--help) usage 0 ;;
    -*) echo "unknown option: $1" >&2; usage 1 ;;
    *) [ -z "$TARGET" ] && TARGET="$1" || { echo "only one project path" >&2; usage 1; }; shift ;;
  esac
done
[ -n "$TARGET" ] || usage 1

ok()   { printf '  \033[32m✓\033[0m %s\n' "$*"; }
warn() { printf '  \033[33m!\033[0m %s\n' "$*"; WARNINGS=$((WARNINGS + 1)); }
fail() { printf '  \033[31m✗\033[0m %s\n' "$*" >&2; exit 1; }
step() { printf '\n\033[1m%s\033[0m\n' "$*"; }
run()  { if [ "$DRY" = 1 ]; then echo "    would: $*"; else "$@"; fi; }
WARNINGS=0
# Run a command for at most $VERIFY_TIMEOUT seconds (exit 124 on timeout); macOS has no timeout(1).
with_timeout() {
  python3 - "$VERIFY_TIMEOUT" "$@" <<'PY'
import os, signal, subprocess, sys
t, cmd = int(sys.argv[1]), sys.argv[2:]
p = subprocess.Popen(cmd, start_new_session=True)
try:
    sys.exit(p.wait(timeout=t))
except subprocess.TimeoutExpired:
    os.killpg(p.pid, signal.SIGKILL)
    sys.exit(124)
PY
}

# ---- 0. Check ---------------------------------------------------------------
step "0. Checking the project"
[ -d "$TARGET" ] || fail "$TARGET doesn't exist"
TARGET="$(cd "$TARGET" && pwd)"
[ "$TARGET" != "$SRC" ] || fail "that's the factory itself; pass your project's path"
ROOT="$(git -C "$TARGET" rev-parse --show-toplevel 2>/dev/null)" || fail "$TARGET isn't a git repo"
[ "$ROOT" = "$TARGET" ] || fail "pass the repo root: $ROOT"
ok "git repo: $TARGET"

python3 -c 'import sys; sys.exit(sys.version_info < (3, 10))' 2>/dev/null \
  && ok "python3 $(python3 -c 'import platform; print(platform.python_version())')" \
  || fail "python3 3.10+ is required"

ORIGIN="$(git -C "$TARGET" remote get-url origin 2>/dev/null || true)"
[ -n "$ORIGIN" ] && ok "origin: $ORIGIN" || warn "no 'origin' remote: the factory pushes branches and opens PRs there"

GH_OK=0
if ! command -v gh >/dev/null; then warn "gh CLI not found: install it and run 'gh auth login'"
elif (cd "$TARGET" && gh auth status >/dev/null 2>&1); then GH_OK=1; ok "gh is logged in"
else warn "gh isn't logged in: run 'gh auth login'"; fi

command -v claude >/dev/null && ok "claude $(claude --version 2>/dev/null | head -1)" \
  || warn "claude CLI not found"

if [ -z "$BASE" ]; then
  BASE="$(git -C "$TARGET" symbolic-ref --short refs/remotes/origin/HEAD 2>/dev/null | sed 's#^origin/##' || true)"
  [ -n "$BASE" ] || BASE="$(git -C "$TARGET" branch --show-current)"
fi
if [ -n "$ORIGIN" ] && ! git -C "$TARGET" ls-remote --exit-code --heads origin "$BASE" >/dev/null 2>&1; then
  warn "branch '$BASE' isn't on origin yet: push it before the first /factory run"
else
  ok "base branch: $BASE"
fi

# ---- 1. Copy ----------------------------------------------------------------
step "1. Copying the factory"
FILES=(.claude/skills/factory/SKILL.md)
for f in "$SRC"/.claude/agents/*.md; do FILES+=(".claude/agents/$(basename "$f")"); done
for f in "$SRC"/factory/bin/*; do [ -f "$f" ] && FILES+=("factory/bin/$(basename "$f")"); done
FILES+=(factory/dashboard.html factory/vendor/mermaid.min.js factory/vendor/mermaid.LICENSE)
for f in "$SRC"/factory/tests/*.py; do FILES+=("factory/tests/$(basename "$f")"); done
for f in "$SRC"/factory/tests/fixtures/*.md; do FILES+=("factory/tests/fixtures/$(basename "$f")"); done

CLASH=()
for f in "${FILES[@]}"; do
  if [ -e "$TARGET/$f" ] && ! cmp -s "$SRC/$f" "$TARGET/$f"; then CLASH+=("$f"); fi
done
if [ ${#CLASH[@]} -gt 0 ]; then
  if [ "$FORCE" = 0 ]; then
    printf '    %s\n' "${CLASH[@]}" >&2
    fail "these files already exist and differ; rerun with --force to replace them (they get backed up)"
  fi
  BACKUP=".factory-backup-$(date +%Y%m%d-%H%M%S)"
  for f in "${CLASH[@]}"; do
    run mkdir -p "$TARGET/$BACKUP/$(dirname "$f")"
    run cp -p "$TARGET/$f" "$TARGET/$BACKUP/$f"
  done
  ok "backed up ${#CLASH[@]} existing files to $BACKUP/"
fi
for f in "${FILES[@]}"; do
  run mkdir -p "$TARGET/$(dirname "$f")"
  run cp "$SRC/$f" "$TARGET/$f"
done
for f in "${FILES[@]}"; do case "$f" in factory/bin/*) run chmod +x "$TARGET/$f" ;; esac; done
ok "${#FILES[@]} files: skill, $(ls "$SRC"/.claude/agents/*.md | wc -l | tr -d ' ') agents, scripts, dashboard, tests"

# config.json: keep the project's; add keys it's missing; set the base branch.
if [ "$DRY" = 1 ]; then
  echo "    would: write or update factory/config.json (base_branch: $BASE)"
else
  python3 - "$SRC/factory/config.json" "$TARGET/factory/config.json" "$BASE" <<'PY'
import json, sys
from pathlib import Path
src, dst, base = json.loads(Path(sys.argv[1]).read_text()), Path(sys.argv[2]), sys.argv[3]
def fill(have, want):
    for k, v in want.items():
        if k not in have: have[k] = v
        elif isinstance(v, dict) and isinstance(have[k], dict): fill(have[k], v)
cfg = json.loads(dst.read_text()) if dst.exists() else {}
existed = dst.exists()
fill(cfg, src)
cfg["base_branch"] = base
dst.write_text(json.dumps(cfg, indent=2) + "\n")
print(f"  \033[32m✓\033[0m factory/config.json {'updated (kept your values)' if existed else 'created'}, base_branch: {base}")
PY
fi

if [ -e "$TARGET/factory/checks.sh" ]; then
  ok "factory/checks.sh kept (yours)"
else
  run cp "$SRC/factory/checks.sh" "$TARGET/factory/checks.sh"
  run chmod +x "$TARGET/factory/checks.sh"
  ok "factory/checks.sh created from the template"
fi

# settings.json: merge the deny list into the project's.
if [ "$DRY" = 1 ]; then
  echo "    would: merge permissions.deny into .claude/settings.json"
else
  python3 - "$SRC/.claude/settings.json" "$TARGET/.claude/settings.json" <<'PY'
import json, sys
from pathlib import Path
want, dst = json.loads(Path(sys.argv[1]).read_text()), Path(sys.argv[2])
have = json.loads(dst.read_text()) if dst.exists() else {}
deny = have.setdefault("permissions", {}).setdefault("deny", [])
added = [d for d in want["permissions"]["deny"] if d not in deny]
deny += added
dst.write_text(json.dumps(have, indent=2) + "\n")
print(f"  \033[32m✓\033[0m .claude/settings.json: {len(added)} deny rules added")
PY
fi

# ---- 2. .gitignore and AGENTS.md -------------------------------------------
step "2. .gitignore and AGENTS.md"
for line in "/factory/jobs/" "/factory/initiatives/" "/.worktrees/" "/.factory-backup-*/"; do
  if ! grep -qxF "$line" "$TARGET/.gitignore" 2>/dev/null; then
    if [ "$DRY" = 1 ]; then echo "    would: add $line to .gitignore"
    else printf '%s\n' "$line" >> "$TARGET/.gitignore"; fi
  fi
done
ok ".gitignore ignores /factory/jobs/, /factory/initiatives/ and /.worktrees/"

SECTION='## Software factory

`/factory run` builds every open GitHub issue labeled `factory`: intake → spec →
build → gate → review → approve, one PR per issue; `/factory <idea>` has the
product owner write an epic and user stories first. State is in `factory/jobs/`
(gitignored); `factory/bin/job` is its only writer. Checks: `factory/checks.sh`.
Dashboard: `cd factory && python3 -m http.server`, then open
http://localhost:8000/dashboard.html.

Each job works in its own git worktree, a full checkout of the repo at
`.worktrees/<job-id>` (gitignored). A tool run from the repo root that doesn'"'"'t
read `.gitignore` sees those copies too; exclude `.worktrees/` in its config.'
DOC=AGENTS.md; [ -e "$TARGET/AGENTS.md" ] || { [ -e "$TARGET/CLAUDE.md" ] && DOC=CLAUDE.md; }
if [ ! -e "$TARGET/$DOC" ]; then
  if [ "$DRY" = 1 ]; then echo "    would: create AGENTS.md"
  else cat > "$TARGET/AGENTS.md" <<EOF
# $(basename "$TARGET")

TODO: what this project is, in two or three sentences.

## Setup

TODO: how to set up a fresh checkout (install dependencies, env files to copy).

## Dev server

TODO: how to start the dev server on a given port, e.g. \`npm run dev -- --port <port>\`.

## Validation

TODO: the test, lint, typecheck and build commands. Keep them in sync with \`factory/checks.sh\`.

$SECTION
EOF
  fi
  warn "created AGENTS.md: fill in its TODOs, every agent reads it"
elif ! grep -q '^## Software factory' "$TARGET/$DOC"; then
  if [ "$DRY" = 1 ]; then echo "    would: append the factory section to $DOC"
  else printf '\n%s\n' "$SECTION" >> "$TARGET/$DOC"; fi
  ok "$DOC: factory section added"
else
  ok "$DOC already has the factory section"
fi

# ---- 3. Labels ----------------------------------------------------------------
step "3. GitHub labels"
if [ "$LABELS" = 0 ]; then ok "skipped (--no-labels)"
elif [ "$GH_OK" = 0 ] || [ -z "$ORIGIN" ]; then warn "skipped: needs gh logged in and an origin; rerun later or create them by hand"
else
  while IFS='|' read -r name color desc; do
    (cd "$TARGET" && run gh label create "$name" --color "$color" --description "$desc" --force >/dev/null)
  done <<'EOF'
factory|5319E7|Build this with the software factory
factory:needs-info|FBCA04|The factory asked the author a question
factory:needs-human|D93F0B|The factory needs a human decision on the PR
factory:draft|EDEDED|A user story in the backlog, not yet in a sprint
epic|3E4B9E|An epic: a group of user stories
EOF
  ok "factory, factory:needs-info, factory:needs-human, factory:draft, epic"
fi

# ---- 4. Verify ----------------------------------------------------------------
step "4. Verifying"
if [ "$DRY" = 1 ]; then ok "skipped (--dry-run)"; else
  python3 -m json.tool "$TARGET/factory/config.json" >/dev/null && ok "config.json is valid JSON"
  "$TARGET/factory/bin/job" --help >/dev/null && ok "factory/bin/job runs"
  [ "$(git -C "$TARGET" check-ignore factory/jobs/x .worktrees/x | wc -l | tr -d ' ')" = 2 ] \
    && ok "factory/jobs/ and .worktrees/ are ignored" || warn "check .gitignore: factory/jobs/ or .worktrees/ isn't ignored"
  LOGDIR="$(mktemp -d)"
  if [ "$CHECKS" = 0 ]; then ok "factory/checks.sh skipped (--no-checks)"
  else
    echo "    running factory/checks.sh (up to ${VERIFY_TIMEOUT}s; --no-checks skips it)..."
    rc=0; (cd "$TARGET" && with_timeout bash factory/checks.sh) >"$LOGDIR/checks.log" 2>&1 </dev/null || rc=$?
    if [ "$rc" = 0 ]; then ok "factory/checks.sh passes on this branch"
    elif [ "$rc" = 124 ]; then warn "factory/checks.sh didn't finish in ${VERIFY_TIMEOUT}s (watch mode or a prompt?): it must exit by itself. See $LOGDIR/checks.log"
    else warn "factory/checks.sh fails here: put your test/lint/build commands in it (the factory can't pass a job until it's green). See $LOGDIR/checks.log"; fi
  fi
  if [ "$TESTS" = 1 ]; then
    echo "    running the factory's tests (about 40s)..."
    rc=0; (cd "$TARGET" && with_timeout python3 -m unittest discover factory/tests) >"$LOGDIR/tests.log" 2>&1 </dev/null || rc=$?
    if [ "$rc" = 0 ]; then ok "factory tests pass ($(grep -o 'Ran [0-9]* tests' "$LOGDIR/tests.log"))"
    else warn "factory tests failed or timed out: see $LOGDIR/tests.log"; fi
  fi
fi

# ---- Done -----------------------------------------------------------------------
step "Done$([ "$DRY" = 1 ] && echo ' (dry run, nothing changed)')"
[ "$WARNINGS" -gt 0 ] && echo "  $WARNINGS warning(s) above."
cat <<EOF
  Next:
    1. Edit factory/checks.sh and AGENTS.md for this project.
    2. Review and commit on '$BASE':  cd "$TARGET" && git add .claude factory .gitignore $DOC && git commit -m "Add software factory"
    3. Push '$BASE', open Claude Code in the project and run /factory run
EOF
