package spec

import (
	"fmt"
	"strings"
	"time"

	"github.com/pablogore/software-factory/internal/domain"
)

// Limits are the length limits a spec must fit in.
type Limits struct {
	Lines, Words, Criteria, CriterionWords int
}

// The limits of specgate on main. A spec over Normal may ask for the
// exception with a reason; within Exception a person decides.
var (
	Normal    = Limits{Lines: 30, Words: 250, Criteria: 4, CriterionWords: 30}
	Exception = Limits{Lines: 45, Words: 400, Criteria: 5, CriterionWords: 45}
)

const (
	exceptionHeading = "Excepción de largo"
	maxReasonWords   = 40
	minCriteria      = 1
	maxValorWords    = 35
	maxListItems     = 3 // Fuera de alcance, Supuestos
	maxDemoSteps     = 4
	maxDiagrams      = 1
	maxDiagramLines  = 15 // inside the ```mermaid fence; doesn't count toward lines/words
)

// Verdict is specgate's answer on a spec.
type Verdict string

// Verdicts.
const (
	VerdictPass      Verdict = "PASS"
	VerdictFail      Verdict = "FAIL"
	VerdictException Verdict = "EXCEPTION"
)

// Report is specgate's verdict with one error per problem. On EXCEPTION the
// errors are the normal limits the spec is over, and Reason is the spec's
// `## Excepción de largo`, its whitespace collapsed.
type Report struct {
	Verdict Verdict
	Errors  []string
	Reason  string
}

// String prints the report as specgate on main does.
func (r Report) String() string {
	var b strings.Builder
	b.WriteString("SPECGATE: " + string(r.Verdict) + "\n")
	for _, e := range r.Errors {
		b.WriteString("- " + e + "\n")
	}
	if r.Verdict == VerdictException {
		b.WriteString("> " + r.Reason + "\n")
	}
	return b.String()
}

// Event is the report as the event Decide takes: an exception is a rejection
// marked as such, so the job waits for a person instead of being rewritten.
func (r Report) Event(job domain.JobID, at time.Time) domain.Event {
	if r.Verdict == VerdictPass {
		return domain.SpecgatePassed{Job: job, At: at}
	}
	return domain.SpecgateFailed{Job: job, Exception: r.Verdict == VerdictException, At: at}
}

// Check runs specgate on a spec. longOK says a person already accepted its
// length exception, which turns an EXCEPTION into a PASS.
func Check(text string, longOK bool) Report {
	errs, reason, asks := checkSpec(text, longOK)
	switch {
	case asks:
		return Report{Verdict: VerdictException, Errors: CheckLimits(text, Normal), Reason: strings.Join(strings.Fields(reason), " ")}
	case len(errs) > 0:
		return Report{Verdict: VerdictFail, Errors: errs}
	default:
		return Report{Verdict: VerdictPass}
	}
}

// checkSpec returns the errors, and the exception's reason when the spec is
// over the normal limits, within the exception ones, and has no OK yet.
func checkSpec(text string, longOK bool) (errs []string, reason string, asks bool) {
	errs = CheckLimits(text, Normal)
	reason, ok := section(text, exceptionHeading)
	if !ok {
		return errs, "", false
	}
	if len(errs) == 0 {
		return nil, "", false // fits anyway: nothing to ask
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return append(errs, fmt.Sprintf("section `## %s` is empty", exceptionHeading)), "", false
	}
	if n := words(reason); n > maxReasonWords {
		return append(errs, fmt.Sprintf("`## %s` has %d words; the maximum is %d", exceptionHeading, n, maxReasonWords)), "", false
	}
	if !strings.HasPrefix(text, "STATUS: READY") {
		return append(errs, fmt.Sprintf("only a `STATUS: READY` spec can ask for `## %s`", exceptionHeading)), "", false
	}
	if errs = CheckLimits(text, Exception); len(errs) > 0 {
		for i := range errs {
			errs[i] += " (with the exception)"
		}
		return errs, "", false
	}
	if longOK {
		return nil, "", false
	}
	return nil, reason, true
}

// CheckLimits returns one error per problem of the spec against lim.
func CheckLimits(text string, lim Limits) []string {
	var errs []string
	add := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }
	first := strings.TrimSpace(strings.SplitN(text, "\n", 2)[0])
	diagrams, text := mermaid(text) // the diagram has its own limit
	if len(diagrams) > maxDiagrams {
		add("%d mermaid diagrams; the maximum is %d", len(diagrams), maxDiagrams)
	}
	for _, d := range diagrams {
		if n := nonBlankLines(d); n > maxDiagramLines {
			add("the mermaid diagram has %d lines; the maximum is %d. Show only the main flow", n, maxDiagramLines)
		}
	}
	// like the word count, the exception's own text doesn't count toward the lines
	lines := strings.Split(strings.TrimRight(removeSections(text, exceptionHeading), "\n"), "\n")
	if first != "STATUS: READY" && first != "STATUS: SPLIT" {
		add("first line must be `STATUS: READY` or `STATUS: SPLIT`, not %s", pyRepr(first))
	}
	if len(lines) > lim.Lines {
		add("%d lines; the maximum is %d", len(lines), lim.Lines)
	}
	if n := words(removeSections(text, "Propuesta de split", "Preguntas", exceptionHeading)); n > lim.Words {
		add("%d words; the maximum is %d. Cut detail or SPLIT the slice", n, lim.Words)
	}
	if criteria, ok := section(text, "Criterios de aceptación"); !ok {
		add("missing section `## Criterios de aceptación` (section names are fixed, not translated)")
	} else {
		crit := items(criteria, true)
		if len(crit) < minCriteria || len(crit) > lim.Criteria {
			add("%d numbered criteria; need %d to %d", len(crit), minCriteria, lim.Criteria)
		}
		for i, c := range crit {
			if n := words(c); n > lim.CriterionWords {
				add("criterion %d has %d words; the maximum is %d (one idea per criterion, plain words)", i+1, n, lim.CriterionWords)
			}
		}
	}
	for _, name := range []string{"Valor", "Demo"} {
		if sec, ok := section(text, name); !ok {
			add("missing section `## %s` (section names are fixed, not translated)", name)
		} else if strings.TrimSpace(sec) == "" {
			add("section `## %s` is empty", name)
		}
	}
	if valor, _ := section(text, "Valor"); words(valor) > maxValorWords {
		add("`## Valor` has %d words; the maximum is %d (one sentence)", words(valor), maxValorWords)
	}
	if demo, _ := section(text, "Demo"); len(items(demo, true)) > maxDemoSteps {
		add("`## Demo` has %d steps; the maximum is %d", len(items(demo, true)), maxDemoSteps)
	}
	for _, name := range []string{"Fuera de alcance", "Supuestos"} {
		if sec, _ := section(text, name); len(items(sec, false)) > maxListItems {
			add("`## %s` has %d items; the maximum is %d", name, len(items(sec, false)), maxListItems)
		}
	}
	return errs
}

func nonBlankLines(s string) int {
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}
