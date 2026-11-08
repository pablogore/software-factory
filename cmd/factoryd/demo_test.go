package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/getsyntegrity/go-specs/specs"
)

type addrRow struct {
	name, addr string
}

func TestWorkerDemoAddr(t *testing.T) {
	specs.Describe(t, "factoryd worker-demo --addr", func(s *specs.Spec) {
		refused := []addrRow{
			{"every IPv4 interface", "0.0.0.0:0"},
			{"an empty host", ":0"},
			{"every IPv6 interface", "[::]:0"},
			{"a LAN address", "192.168.1.10:8080"},
			{"a host name other than localhost", "example.com:80"},
		}
		specs.Table(s, refused, func(r addrRow) string {
			return "refuses " + r.name + " without listening"
		}, func(ctx *specs.Context, r addrRow) {
			var out bytes.Buffer

			err := workerDemo(context.Background(), []string{"--addr", r.addr}, &out)

			ctx.Expect(err).To(specs.MatchError(errNotLoopback))
			ctx.Expect(err.Error()).To(specs.Contain(r.addr))
			ctx.Expect(out.String()).To(specs.BeEmpty())
		})

		allowed := []addrRow{
			{"127.0.0.1", "127.0.0.1:0"},
			{"another 127/8 address", "127.0.0.2:0"},
			{"::1", "[::1]:0"},
			{"localhost", "localhost:0"},
		}
		specs.Table(s, allowed, func(r addrRow) string {
			return "accepts " + r.name
		}, func(ctx *specs.Context, r addrRow) {
			ctx.Expect(loopback(r.addr)).To(specs.BeNil())
		})

		s.It("serves on 127.0.0.1 as before", func(ctx *specs.Context) {
			run, stop := context.WithCancel(context.Background())
			pr, pw := io.Pipe()
			done := make(chan error, 1)
			state := ctx.Testing().TempDir()
			go func() {
				done <- workerDemo(run, []string{"--addr", "127.0.0.1:0", "--dev-state", state}, pw)
				_ = pw.Close()
			}()

			first, err := bufio.NewReader(pr).ReadString('\n')
			stop()
			go func() { _, _ = io.Copy(io.Discard, pr) }()

			ctx.Expect(err).To(specs.BeNil())
			ctx.Expect(first).To(specs.StartWith("worker demo on http://127.0.0.1:"))
			ctx.Expect(<-done).To(specs.BeNil())
		})
	})
}
