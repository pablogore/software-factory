//go:build unix

package devserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// entry is the record of a running server: its group and when its leader
// started, which tells it apart from a later process given the same pid.
type entry struct {
	PGID    int    `json:"pgid"`
	Started string `json:"started"`
}

// Registered returns Servers like New's that keep a record of each running
// server in dir, first killing every server recorded there by an earlier
// run, as a crash leaves them, with its whole group. A record whose pid is
// now another process is dropped and that process left alone.
func Registered(ctx context.Context, grace time.Duration, dir string) (*Servers, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("dev server records: %w", err)
	}
	s := New(grace)
	s.dir = dir
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("dev server records: %w", err)
	}
	var errs []error
	for _, f := range files {
		errs = append(errs, s.reap(ctx, f))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("killing dev servers left by an earlier run: %w", err)
	}
	return s, nil
}

// reap kills the group recorded in file, if it is still the one recorded,
// and removes the record.
func (s *Servers) reap(ctx context.Context, file string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	var e entry
	if err := json.Unmarshal(data, &e); err != nil || e.PGID < 2 {
		return fmt.Errorf("%s: %w", file, errors.Join(err, errors.New("not a dev server record")))
	}
	started, err := startedAt(ctx, e.PGID)
	if err != nil {
		return err
	}
	// While any member of the group lives, no new process gets its id, so a
	// live group whose leader is gone is still the recorded one.
	if started == e.Started || started == "" {
		if err := s.stopOrphan(ctx, e.PGID); err != nil {
			return err
		}
	}
	return os.Remove(file)
}

// stopOrphan stops a group this process did not start, so it can't reap
// its leader: the group is gone once no member answers a signal.
func (s *Servers) stopOrphan(ctx context.Context, pgid int) error {
	srv := &server{pgid: pgid, done: make(chan struct{})}
	close(srv.done)
	return s.stop(ctx, srv)
}

// record saves pgid's entry, when s keeps records.
func (s *Servers) record(ctx context.Context, pgid int) error {
	if s.dir == "" {
		return nil
	}
	started, err := startedAt(ctx, pgid)
	if err != nil {
		return err
	}
	if started == "" {
		return fmt.Errorf("process %d is gone", pgid)
	}
	data, err := json.Marshal(entry{PGID: pgid, Started: started})
	if err != nil {
		return err
	}
	return os.WriteFile(s.file(pgid), data, 0o600)
}

// forget removes pgid's entry, once its group is gone.
func (s *Servers) forget(pgid int) error {
	if s.dir == "" {
		return nil
	}
	if err := os.Remove(s.file(pgid)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Servers) file(pgid int) string {
	return filepath.Join(s.dir, strconv.Itoa(pgid)+".json")
}

// startedAt is when process pid started, as ps tells it, or "" when there
// is no such process.
func startedAt(ctx context.Context, pid int) (string, error) {
	cmd := exec.CommandContext(ctx, "ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	cmd.Env = []string{"LC_ALL=C", "PATH=" + os.Getenv("PATH")}
	out, err := cmd.Output()
	if exit, ok := errors.AsType[*exec.ExitError](err); ok && exit.ExitCode() == 1 && strings.TrimSpace(string(out)) == "" {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("ps of process %d: %w", pid, err)
	}
	return strings.TrimSpace(string(out)), nil
}
