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

Hands the role's result, a JSON value read from FILE or stdin, to factoryd
as the job's result. The job token comes from $` + worker.TokenEnv + `; --url
defaults to $` + urlEnv + ` and --job to $` + jobEnv + `.
`

// runWorker runs `factory worker …` with args after "worker" and returns the
// exit code: 0 when factoryd took the result, 1 when it refused it or could
// not be reached, 2 for a usage error.
func runWorker(ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "submit-result" {
		_, _ = fmt.Fprint(stderr, workerUsage)
		return 2
	}
	fs := flag.NewFlagSet("submit-result", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { _, _ = fmt.Fprint(stderr, workerUsage) }
	url := fs.String("url", getenv(urlEnv), "factoryd's URL")
	job := fs.String("job", getenv(jobEnv), "the job's ID")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	token := getenv(worker.TokenEnv)
	switch {
	case fs.NArg() > 1:
		_, _ = fmt.Fprint(stderr, workerUsage)
		return 2
	case token == "":
		_, _ = fmt.Fprintf(stderr, "factory worker submit-result: no job token: set %s\n", worker.TokenEnv)
		return 2
	case *url == "":
		_, _ = fmt.Fprintf(stderr, "factory worker submit-result: no factoryd URL: set %s or --url\n", urlEnv)
		return 2
	case *job == "":
		_, _ = fmt.Fprintf(stderr, "factory worker submit-result: no job: set %s or --job\n", jobEnv)
		return 2
	}
	result, err := readResult(fs.Arg(0), stdin)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "factory worker submit-result: %v\n", err)
		return 1
	}
	a, err := worker.Client{URL: *url, Token: token}.SubmitResult(ctx, *job, result)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "factory worker submit-result: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "result recorded for job %s: %s\n", a.Job, strings.Join(a.Events, ", "))
	return 0
}

func readResult(file string, stdin io.Reader) ([]byte, error) {
	if file == "" || file == "-" {
		return io.ReadAll(stdin)
	}
	return os.ReadFile(file)
}
