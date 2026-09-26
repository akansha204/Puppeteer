package driver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const (
	defaultGrace = 3 * time.Second
	killTimeout  = 2 * time.Second
)

type Spec struct {
	Path string
	Args []string
	Cwd  string
	Env  []string
}

// ExitResult describes how a process actually died, independent of why the
// caller might have wanted it to die. For a normal exit Err is nil, ExitCode
// is the status and Signal is -1; for a signal death Err is non-nil, Signal is
// the fatal signal and ExitCode is -1.
type ExitResult struct {
	Err      error
	ExitCode int
	Signal   syscall.Signal
}

type Handle struct {
	PID int

	done  chan struct{} //closed by Wait when the process dies
	proc  *os.Process
	cmd   *exec.Cmd
	stdin io.WriteCloser // optional; Write feeds the process here
}

type Driver interface {
	Start(ctx context.Context, spec Spec) (*Handle, error)
	Write(h *Handle, data []byte) (int, error)
	Resize(h *Handle, rows, cols uint16) error
	Stop(ctx context.Context, h *Handle) error
	Wait(h *Handle) ExitResult
}

type ProcessDriver struct {
	Grace time.Duration
}

func NewProcessDriver() *ProcessDriver {
	return &ProcessDriver{Grace: defaultGrace}
}

func (d *ProcessDriver) Start(ctx context.Context, spec Spec) (*Handle, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, spec.Path, spec.Args...)
	cmd.Dir = spec.Cwd
	cmd.Env = spec.Env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return &Handle{
		PID:   cmd.Process.Pid,
		done:  make(chan struct{}),
		proc:  cmd.Process,
		cmd:   cmd,
		stdin: stdin,
	}, nil
}

func (d *ProcessDriver) Write(h *Handle, data []byte) (int, error) {
	if h.stdin == nil {
		return 0, fmt.Errorf("process %d has no stdin", h.PID)
	}
	return h.stdin.Write(data)
}

// Resize is a no-op for a plain process: a window size is only meaningful
// once a PTY driver puts the process on a terminal (TIOCSWINSZ)(Set this terminal's window size).
func (d *ProcessDriver) Resize(_ *Handle, _, _ uint16) error {
	return nil
}

func (d *ProcessDriver) Wait(h *Handle) ExitResult {
	err := h.cmd.Wait()
	h.proc = nil
	h.cmd = nil
	h.stdin = nil
	close(h.done)

	res := ExitResult{Err: err}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			res.ExitCode = ws.ExitStatus()
			res.Signal = ws.Signal()
		}
	}
	return res
}

// Stop escalates SIGTERM to SIGKILL against the whole process group. A
// cancelled context bounds the wait but never abandons the group half-signalled.
func (d *ProcessDriver) Stop(ctx context.Context, h *Handle) error {
	if ctx == nil {
		ctx = context.Background()
	}

	grace := d.Grace
	if grace <= 0 {
		grace = defaultGrace
	}

	pgid := -h.PID
	if err := syscall.Kill(pgid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("signal process group: %w", err)
	}

	select {
	case <-h.done:
		return nil
	case <-time.After(grace):
	case <-ctx.Done():
	}

	if err := syscall.Kill(pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill process group: %w", err)
	}

	select {
	case <-h.done:
		return nil
	case <-time.After(killTimeout):
		return fmt.Errorf("process group %d did not terminate after SIGKILL", h.PID)
	case <-ctx.Done():
		return ctx.Err()
	}
}
