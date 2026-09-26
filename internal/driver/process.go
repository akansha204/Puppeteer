package driver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
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

// ErrClosed reports a handle whose process has been reaped.
var ErrClosed = errors.New("handle closed")

type Handle struct {
	PID int

	// stateMu guards the io fields against Wait's teardown; readMu and
	// writeMu keep one read running alongside one write (full-duplex).
	stateMu sync.RWMutex
	readMu  sync.Mutex
	writeMu sync.Mutex

	done   chan struct{} //closed by Wait when the process dies
	proc   *os.Process
	cmd    *exec.Cmd
	stdin  io.WriteCloser // optional; Write feeds the process here
	stdout io.ReadCloser  // optional; Read drains the process here
	master *os.File       // optional; the PTY master when on a terminal
}

type Driver interface {
	Start(ctx context.Context, spec Spec) (*Handle, error)
	Write(h *Handle, data []byte) (int, error)
	Read(h *Handle, p []byte) (int, error)
	ReadTimeout(h *Handle, p []byte, timeout time.Duration) (int, error)
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
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return &Handle{
		PID:    cmd.Process.Pid,
		done:   make(chan struct{}),
		proc:   cmd.Process,
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
	}, nil
}

func (d *ProcessDriver) Write(h *Handle, data []byte) (int, error) {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()

	// Held across the write so Wait cannot close the file mid-write.
	h.stateMu.RLock()
	defer h.stateMu.RUnlock()

	if h.stdin == nil {
		return 0, fmt.Errorf("%w: process %d has no stdin", ErrClosed, h.PID)
	}
	return h.stdin.Write(data)
}

func (d *ProcessDriver) Read(h *Handle, p []byte) (int, error) {
	h.readMu.Lock()
	defer h.readMu.Unlock()

	h.stateMu.RLock()
	defer h.stateMu.RUnlock()

	if h.stdout == nil {
		return 0, fmt.Errorf("%w: process %d has no stdout", ErrClosed, h.PID)
	}
	return h.stdout.Read(p)
}

func (d *ProcessDriver) ReadTimeout(h *Handle, p []byte, timeout time.Duration) (int, error) {
	h.readMu.Lock()
	defer h.readMu.Unlock()

	// Held across the timeout so Wait cannot close the file mid-read.
	h.stateMu.RLock()
	defer h.stateMu.RUnlock()

	f := h.master
	if f == nil {
		var ok bool
		f, ok = h.stdout.(*os.File)
		if !ok || f == nil {
			return 0, fmt.Errorf("%w: process %d has no pollable output", ErrClosed, h.PID)
		}
	}

	fd := int(f.Fd()) // non-blocking so the read under select returns immediately
	if err := syscall.SetNonblock(fd, true); err != nil {
		return 0, err
	}
	defer syscall.SetNonblock(fd, false)

	rfds := &syscall.FdSet{} // watch fd: word fd/64, bit fd%64
	rfds.Bits[fd/64] = 1 << (fd % 64)
	tv := &syscall.Timeval{
		Sec:  int64(timeout / time.Second),
		Usec: int64(timeout%time.Second) / int64(time.Microsecond),
	}
	for {
		n, err := syscall.Select(fd+1, rfds, nil, nil, tv)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return 0, err
		}
		if n == 0 || rfds.Bits[fd/64]&(1<<(fd%64)) == 0 {
			return 0, nil
		}
		break
	}

	for attempt := 0; ; attempt++ {
		n, err := f.Read(p)
		if err == syscall.EAGAIN && attempt < 3 {
			continue
		}
		return n, err
	}
}

// Resize is a no-op for a plain process: a window size is only meaningful
// once a PTY driver puts the process on a terminal (TIOCSWINSZ)(Set this terminal's window size).
func (d *ProcessDriver) Resize(_ *Handle, _, _ uint16) error {
	return nil
}

func (d *ProcessDriver) Wait(h *Handle) ExitResult {
	// cmd.Wait runs outside the locks; only teardown locks, once dead.
	err := h.cmd.Wait()
	h.stateMu.Lock()
	h.proc = nil
	h.cmd = nil
	h.stdin = nil
	h.stdout = nil
	if h.master != nil {
		h.master.Close()
		h.master = nil
	}
	h.stateMu.Unlock()
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
