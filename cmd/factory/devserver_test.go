package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/getsyntegrity/go-specs/specs"

	"github.com/pablogore/software-factory/internal/adapters/out/devserver"
	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/gatelock"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/platform/portrange"
	"github.com/pablogore/software-factory/internal/ports"
	"github.com/pablogore/software-factory/internal/wiring"
)

// helperEnv makes TestDevServerHelper, run as a repo's dev server command,
// serve the app on $PORT until it is killed. The repo's dev server config
// sets it, since nothing else of the test's environment reaches the server.
const helperEnv = "FACTORY_TEST_DEVSERVER"

func TestDevServerHelper(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		return
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+os.Getenv("PORT"))
	if err != nil {
		t.Fatal(err)
	}
	_ = http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// A secret of factoryd's environment would show here.
		_, _ = io.WriteString(w, "the job's app"+os.Getenv("GITHUB_TOKEN"))
	}))
}

type oneDir struct{ dir string }

func (d oneDir) Prepare(_ context.Context, job domain.JobID) (ports.Worktree, error) {
	return ports.Worktree{Job: job, Path: d.dir}, nil
}

func (oneDir) Clean(context.Context, domain.JobID) error { return nil }

// served is a worker API whose job j-1 is in build in a git repo whose
// .factory/devserver.yaml runs TestDevServerHelper, with tokens for a
// builder and a security reviewer that both list dev_server.
type served struct {
	url               string
	builder, security string
	// base is the first port of j-1's range, which has 10.
	base int
}

func serve(ctx *specs.Context) served {
	t := ctx.Testing()
	t.Setenv("GITHUB_TOKEN", "ghp_factoryd_secret")
	dir := t.TempDir()
	ctx.Expect(os.MkdirAll(filepath.Join(dir, ".factory"), 0o755)).To(specs.BeNil())
	ctx.Expect(os.WriteFile(filepath.Join(dir, wiring.DevServerFile), []byte("command: "+os.Args[0]+" -test.run=^TestDevServerHelper$\nenv:\n  "+helperEnv+": \"1\"\n"), 0o600)).To(specs.BeNil())
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "dev server"}} {
		ctx.Expect(exec.Command("git", append([]string{"-C", dir}, args...)...).Run()).To(specs.BeNil())
	}
	head, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	ctx.Expect(err).To(specs.BeNil())

	store, err := memstore.New()
	ctx.Expect(err).To(specs.BeNil())
	ctx.Expect(store.Create(context.Background(), domain.Job{ID: "j-1", Title: "Job", Pipeline: "default", Stage: "build", Round: 1})).To(specs.BeNil())
	ctx.Expect(store.RecordBase(context.Background(), "j-1", strings.TrimSpace(string(head)))).To(specs.BeNil())
	tokens := jobtoken.New()
	tools := []string{"submit_result", "progress", "dev_server"}
	s := served{}
	s.builder, err = tokens.Issue(jobtoken.Grant{Job: "j-1", Role: "builder", Kind: domain.KindBuild, Tools: tools, Round: 1})
	ctx.Expect(err).To(specs.BeNil())
	s.security, err = tokens.Issue(jobtoken.Grant{Job: "j-1", Role: "reviewer-security", Kind: domain.KindReview, Tools: tools, Round: 1})
	ctx.Expect(err).To(specs.BeNil())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	ctx.Expect(err).To(specs.BeNil())
	base := ln.Addr().(*net.TCPAddr).Port
	ctx.Expect(ln.Close()).To(specs.BeNil())
	s.base = base
	ranges, err := portrange.New(base, 10)
	ctx.Expect(err).To(specs.BeNil())
	servers := devserver.New(time.Second)
	ctx.Cleanup(func() { _ = servers.StopJob(context.Background(), "j-1") })
	dev := &wiring.DevServers{Servers: servers, Worktrees: oneDir{dir}, Ports: ranges, Bases: store}
	srv := httptest.NewServer(wiring.WorkerAPI(tokens, store, onePipeline{}, &gate{}, gatelock.New(), stopped{}, dev))
	ctx.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

func (s served) env(token string) map[string]string {
	return map[string]string{"FACTORY_URL": s.url, "FACTORY_JOB_ID": "j-1", "FACTORY_JOB_TOKEN": token}
}

// portOf is the port a dev-server run printed.
func portOf(ctx *specs.Context, r run) int {
	_, after, ok := strings.Cut(r.stdout, " on port ")
	ctx.Expect(ok).ToEqual(true)
	port, err := strconv.Atoi(strings.SplitN(after, ":", 2)[0])
	ctx.Expect(err).To(specs.BeNil())
	return port
}

// page gets the app on port, waiting up to a few seconds for it to listen.
func page(port int) (string, error) {
	var err error
	for range 100 {
		var resp *http.Response
		resp, err = http.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/")
		if err == nil {
			body, rerr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			return string(body), rerr
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "", err
}

// down tells whether nothing answers on port.
func down(port int) bool {
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), time.Second)
	if err == nil {
		_ = c.Close()
	}
	return err != nil
}

func TestWorkerDevServer(t *testing.T) {
	specs.Describe(t, "factory worker dev-server", func(s *specs.Spec) {
		s.It("starts the builder's dev server on a port of the job's range, where the app answers without factoryd's secrets", func(ctx *specs.Context) {
			f := serve(ctx)

			r := cli(f.env(f.builder), "", "dev-server")

			ctx.Expect(r.code).ToEqual(0)
			port := portOf(ctx, r)
			ctx.Expect(port >= f.base && port < f.base+10).ToEqual(true)
			ctx.Expect(r.stdout).ToEqual("dev server of job j-1 on port " + strconv.Itoa(port) + ": http://localhost:" + strconv.Itoa(port) + "\n")
			body, err := page(port)
			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(body).ToEqual("the job's app")
		})

		s.It("stops it with --stop, leaving nothing on its port", func(ctx *specs.Context) {
			f := serve(ctx)
			port := portOf(ctx, cli(f.env(f.builder), "", "dev-server"))
			_, err := page(port)
			ctx.Expect(err).To(specs.BeNil())

			r := cli(f.env(f.builder), "", "dev-server", "--stop")

			ctx.Expect(r.code).ToEqual(0)
			ctx.Expect(r.stdout).ToEqual("dev server of job j-1 stopped\n")
			ctx.Expect(down(port)).ToEqual(true)
		})

		s.It("stops it once the builder hands in its result", func(ctx *specs.Context) {
			f := serve(ctx)
			port := portOf(ctx, cli(f.env(f.builder), "", "dev-server"))
			_, err := page(port)
			ctx.Expect(err).To(specs.BeNil())

			r := cli(f.env(f.builder), `{"status":"READY"}`, "submit-result")

			ctx.Expect(r.code).ToEqual(0)
			ctx.Expect(down(port)).ToEqual(true)
		})

		s.It("refuses a security reviewer that lists dev_server, starting nothing", func(ctx *specs.Context) {
			f := serve(ctx)

			r := cli(f.env(f.security), "", "dev-server")

			ctx.Expect(r.code).ToEqual(1)
			ctx.Expect(r.stderr).ToEqual("factory worker dev-server: refused (403): worker: dev server: job token not allowed: role \"reviewer-security\" may not use tool \"dev_server\"\n")
			ctx.Expect(r.stdout).ToEqual("")
		})
	})
}
