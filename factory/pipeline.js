// A job's pipeline: intake → spec → build → one step per routed reviewer → approver.
// Pure functions, shared by dashboard.html and factory/tests/test_pipeline.py (run with node).

// The tone of a factory file, from its first line.
function tone(first) {
  if (/^(VERDICT:\s*PASS|GATE:\s*PASS|APPROVE|STATUS:\s*READY|DECISION:\s*ACCEPT)$/.test(first)) return 'good'
  if (/^(VERDICT:\s*CHANGES|DECISION:\s*NEEDS-INFO)$/.test(first)) return 'warn'
  if (/^(ESCALATE|REJECTED|STATUS:\s*BLOCKED|GATE:\s*FAIL|DECISION:\s*REJECT)$/.test(first)) return 'stop'
  return null
}

// The step a job's stage belongs to; stages not listed (paused, triage, merged…) have none.
const STEP_OF_STAGE = {
  intake: 'intake', 'needs-info': 'intake', initiative: 'intake',
  spec: 'spec', rate: 'spec', replan: 'spec', checkpoint: 'spec',
  build: 'build', gate: 'build',
  review: 'review',
  approve: 'approver', merging: 'approver', 'needs-human': 'approver',
}
const RUNNING_STAGES = new Set(['intake', 'initiative', 'spec', 'rate', 'replan', 'build', 'gate', 'review', 'approve', 'merging'])
const STATE_OF_TONE = { good: 'ok', warn: 'changes', stop: 'failed' }

// firstLines maps a path inside the job folder ("spec.md", "round-1/review-code.md")
// to its first line; a missing path means the file doesn't exist.
// Each step: { id, label, doc (null when the file doesn't exist), state, current }.
// state is pending, running, ok, changes or failed.
function pipelineSteps(job, firstLines) {
  const round = job.round || 0
  const dir = `round-${round}/`
  const steps = [
    { id: 'intake', label: 'intake', path: 'intake.md' },
    { id: 'spec', label: 'spec', path: 'spec.md' },
    { id: 'build', label: 'build', path: 'build.md' },
    ...Object.keys(job.agents?.reviewers || {}).map((r) =>
      ({ id: 'review', label: r, path: `${dir}review-${r}.md` })),
    { id: 'approver', label: 'approver', path: 'decision.md' },
  ]
  const at = STEP_OF_STAGE[job.stage] || null
  const running = RUNNING_STAGES.has(job.stage)
  const gateFailed = (firstLines[`${dir}checks.md`] || '').trim() === 'GATE: FAIL'
  return steps.map(({ id, label, path }) => {
    const first = firstLines[path]
    const doc = first === undefined ? null : path
    const current = id === at
    let state = doc ? STATE_OF_TONE[tone(first.trim())] || 'ok' : 'pending'
    // Reviewers write once per round, so a reviewer with a verdict is done even while others run.
    if (current && running && !(id === 'review' && doc)) state = 'running'
    else if (id === 'build' && gateFailed) state = 'failed'
    return { id, label, doc, state, current }
  })
}

if (typeof module !== 'undefined') module.exports = { tone, pipelineSteps }
