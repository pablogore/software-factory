package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pablogore/software-factory/pkg/worker"
)

// The environment a session's agent runs with, besides worker.TokenEnv.
const (
	urlEnv = "FACTORY_URL"
	jobEnv = "FACTORY_JOB_ID"
)

const workerUsage = `usage: factory worker submit-result [--url URL] [--job ID] [FILE]
       factory worker run-gate [--url URL] [--job ID]
       factory worker ask-human [--url URL] [--job ID] --to product|tech [--option TEXT]... QUESTION

submit-result hands the role's result, a JSON value read from FILE or stdin,
to factoryd as the job's result. run-gate runs the job's checks, as the gate
will after the result is handed in, and prints whether they pass, their
output and the rules the diff breaks; it exits 0 only when they pass.
ask-human asks a person QUESTION, for the product or the tech person, with
one --option per answer they may choose from: the job waits for the answer
and your stage runs again with it, so hand in no result and end your turn.
The job token comes from $` + worker.TokenEnv + `; --url defaults to $` + urlEnv + ` and
--job to $` + jobEnv + `.
`

// runWorker runs `factory worker …` with args after "worker" and returns the
// exit code: 0 when factoryd took the result or the question, or ran the
// checks and they pass, 1 when it refused the call, the checks fail or it could not be
// reached, 2 for a usage error.
func runWorker(ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "submit-result" && args[0] != "run-gate" && args[0] != "ask-human") {
		_, _ = fmt.Fprint(stderr, workerUsage)
		return 2
	}
	cmd := args[0]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { _, _ = fmt.Fprint(stderr, workerUsage) }
	url := fs.String("url", getenv(urlEnv), "factoryd's URL")
	job := fs.String("job", getenv(jobEnv), "the job's ID")
	var q worker.Question
	if cmd == "ask-human" {
		fs.StringVar(&q.To, "to", "", "who should answer: product or tech")
		fs.Func("option", "an answer the person may choose from; repeat it for each", func(o string) error {
			q.Options = append(q.Options, o)
			return nil
		})
	}
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	token := getenv(worker.TokenEnv)
	minArgs, maxArgs := 0, 1
	switch cmd {
	case "run-gate":
		maxArgs = 0
	case "ask-human":
		minArgs = 1
	}
	switch {
	case fs.NArg() < minArgs || fs.NArg() > maxArgs:
		_, _ = fmt.Fprint(stderr, workerUsage)
		return 2
	case token == "":
		_, _ = fmt.Fprintf(stderr, "factory worker %s: no job token: set %s\n", cmd, worker.TokenEnv)
		return 2
	case *url == "":
		_, _ = fmt.Fprintf(stderr, "factory worker %s: no factoryd URL: set %s or --url\n", cmd, urlEnv)
		return 2
	case *job == "":
		_, _ = fmt.Fprintf(stderr, "factory worker %s: no job: set %s or --job\n", cmd, jobEnv)
		return 2
	}
	client := worker.Client{URL: *url, Token: token}
	switch cmd {
	case "run-gate":
		return runGate(ctx, client, *job, stdout, stderr)
	case "ask-human":
		q.Question = fs.Arg(0)
		return askHuman(ctx, client, *job, q, stdout, stderr)
	}
	result, err := readResult(fs.Arg(0), stdin)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "factory worker submit-result: %v\n", err)
		return 1
	}
	a, err := client.SubmitResult(ctx, *job, result)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "factory worker submit-result: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "result recorded for job %s: %s\n", a.Job, strings.Join(a.Events, ", "))
	return 0
}

// runGate prints the run of job's checks as the MCP tool run_gate answers
// it, and exits 0 only when they pass.
func runGate(ctx context.Context, client worker.Client, job string, stdout, stderr io.Writer) int {
	g, err := client.RunGate(ctx, job)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "factory worker run-gate: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprint(stdout, g.Report)
	if !g.Passed {
		return 1
	}
	return 0
}

// askHuman asks a person q on job and prints what factoryd saved, worded as
// the MCP tool ask_human answers it.
func askHuman(ctx context.Context, client worker.Client, job string, q worker.Question, stdout, stderr io.Writer) int {
	a, err := client.AskHuman(ctx, job, q)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "factory worker ask-human: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "question recorded for job %s: %s. The job waits for a person's answer and your stage runs again with it; "+
		"hand in no result and end your turn.\n", a.Job, strings.Join(a.Events, ", "))
	return 0
}

func readResult(file string, stdin io.Reader) ([]byte, error) {
	if file == "" || file == "-" {
		return io.ReadAll(stdin)
	}
	return os.ReadFile(file)
}
