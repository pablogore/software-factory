# Factory improvement notes

## reviewer-security: an open security finding must block (2026-11-03, job 001-vmd-015-1-implement, #80 / PR #463)

What happened: in round 2 the security reviewer returned `VERDICT: PASS` while
`round-2/review-security.md` listed, as a non-blocking note, that the client's
`Forwarded` / `X-Forwarded-*` headers were passed to the upstream unchanged
(a client could forge its IP, host or protocol). The operator considered that
blocking, and the approver then returned APPROVE with the issue still open.
The header problem was fixed by hand in 0f54ffa.

Note on the record: there was no round-1 security review (round 1 ended at a
BLOCKED build because `checks.sh` was a placeholder), so the finding was not
"raised in round 1 and then ignored". It was raised in round 2 and rated
non-blocking, partly because the spec (acceptance criterion 1, "forwarded as it
came") required the pass-through.

Changes to make:
- reviewer-security prompt: a security finding that is not fixed is blocking
  (`VERDICT: CHANGES`), even when the spec seems to require the behavior. In
  that case the reviewer should also say the spec is the problem, so the
  replan/operator can change it.
- approver prompt: do not APPROVE while a review lists an unresolved security
  finding as a "note".
- State machine: once every review passes, `job advance` refuses build and
  needs-human, so the operator cannot send feedback and the only forward step
  is the approver (which can merge). Consider letting `job feedback` reopen a
  job at `review`/`approve`.

## specgate rejected a spec with translated headings (2026-11-04, job 008, #472): fixed

What happened: with `language: en` the spec-writer translated the template
headings, and `specgate` failed with "missing section `## Criterios de
aceptación`" and "`## Valor`". The retry cost a spec-writer run.

Fix: the spec-writer prompt now lists the five fixed headings and says they stay
as they are even when `language` is `en`; the specgate error says section names
are fixed, not translated.

## `job queue` skipped a job whose PR closed the issue (2026-11-04, job 004, #81): fixed

What happened: the PR said `Closes #81`, so merging it closed the issue. The job
sat at `replan`, and `job queue` printed `skip ... issue is closed or no longer
labeled factory`, so the replan never resumed. `job replan` had to be forced by
hand.

Fix: a job at `replan` or `merging` whose issue is closed is a `resume` row, since
the merge itself closes the issue. Covered by
`test_queue_resumes_a_replan_whose_issue_the_merge_closed`.

Still open: a multi-slice initiative whose first PR says `Closes #n` closes the
issue before the later slices run. `pr_ref` should be `Refs` for every slice but
the last.
