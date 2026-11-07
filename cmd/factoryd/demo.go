package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/wiring"
)

// demoJobs wait in build for a builder's result.
var demoJobs = []domain.JobID{"demo-1", "demo-2"}

type demoPipelines struct{}

// Pipeline is a build stage followed by a review, so a builder's result
// moves the job on.
func (demoPipelines) Pipeline(_ context.Context, name string) (domain.Pipeline, error) {
	if name != "demo" {
		return domain.Pipeline{}, fmt.Errorf("no pipeline %q", name)
	}
	return domain.Pipeline{Name: "demo", Stages: []domain.Stage{
		{Name: "build", Kind: domain.KindBuild, Roles: []string{"builder"}},
		{Name: "review", Kind: domain.KindReview, Roles: []string{"reviewer-code"}, OnChanges: "build"},
	}}, nil
}

// workerDemo serves the worker API over an in-memory store with demoJobs,
// until ctx ends. It is not a factoryd server: it prints a builder's token
// per job and, after every request, each job's stage and saved events, so a
// person can try `factory worker submit-result` against it.
func workerDemo(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("worker-demo", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:0", "address to listen on")
	if err := fs.Parse(args); err != nil {
		return err
	}
	store, err := memstore.New()
	if err != nil {
		return err
	}
	tokens := jobtoken.New()
	issued := map[domain.JobID]string{}
	for _, id := range demoJobs {
		if err := store.Create(ctx, domain.Job{ID: id, Title: "Demo job", Pipeline: "demo", Stage: "build", Round: 1}); err != nil {
			return err
		}
		if issued[id], err = tokens.Issue(jobtoken.Grant{Job: id, Role: "builder", Kind: domain.KindBuild}); err != nil {
			return err
		}
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", *addr)
	if err != nil {
		return err
	}
	url := "http://" + ln.Addr().String()
	_, _ = fmt.Fprintf(out, "worker demo on %s; jobs %s wait in build for a builder's result\n", url, joinIDs(demoJobs))
	_, _ = fmt.Fprintf(out, "\nexport FACTORY_URL=%s\n", url)
	for _, id := range demoJobs {
		_, _ = fmt.Fprintf(out, "# job %s\nexport FACTORY_JOB_ID=%s FACTORY_JOB_TOKEN=%s\n", id, id, issued[id])
	}
	_, _ = fmt.Fprintln(out)

	api := wiring.WorkerAPI(tokens, store, demoPipelines{}, wiring.SystemClock{})
	var mu sync.Mutex
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.ServeHTTP(w, r)
		mu.Lock()
		defer mu.Unlock()
		_, _ = fmt.Fprintf(out, "%s %s\n", r.Method, r.URL.Path)
		for _, id := range demoJobs {
			job, _, _ := store.Load(ctx, id)
			hist, _ := store.History(ctx, id)
			names := make([]string, len(hist))
			for i, h := range hist {
				names[i] = h.Event.EventName()
			}
			_, _ = fmt.Fprintf(out, "  %s: stage %s, events [%s]\n", id, job.Stage, strings.Join(names, ", "))
		}
	})}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func joinIDs(ids []domain.JobID) string {
	s := make([]string, len(ids))
	for i, id := range ids {
		s[i] = string(id)
	}
	return strings.Join(s, " and ")
}
