#!/usr/bin/env bash
# The gate runs this from the main checkout with the job's worktree as cwd.
# Go base (arch/factoryd-go): make check. Python base (main): factory/tests.
set -euo pipefail

ran=0
if [ -f Makefile ]; then
  make check
  ran=1
fi
if [ -d factory/tests ]; then
  python3 -m unittest discover -s factory/tests -p 'test_*.py' -v
  ran=1
fi
if [ "$ran" = 0 ]; then
  echo "factory/checks.sh: no Makefile and no factory/tests in $(pwd); nothing to check" >&2
  exit 1
fi
