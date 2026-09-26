package driver

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const (
	defaultGrace = 3 * time.Second
	killTimeout  = 2 * time.Second
)

type Command struct {
	Path string
	Args []string
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

	done chan struct{} //closed by Wait when the process dies
	proc *os.Process
	cmd  *exec.Cmd
}

type Driver interface {
	Start(Command) (*Handle, error)
	Wait(h *Handle) ExitResult
	Stop(h *Handle) error
}

type ProcessDriver struct {
	Grace time.Duration
}

func NewProcessDriver() *ProcessDriver {
	return &ProcessDriver{Grace: defaultGrace}
}

func (d *ProcessDriver) Start(c Command) (*Handle, error) {
	cmd := exec.Command(c.Path, c.Args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Handle{
		PID:  cmd.Process.Pid,
		done: make(chan struct{}),
		proc: cmd.Process,
		cmd:  cmd,
	}, nil
}

func (d *ProcessDriver) Wait(h *Handle) ExitResult {
	err := h.cmd.Wait()
	h.proc = nil
	h.cmd = nil
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

func (d *ProcessDriver) Stop(h *Handle) error {
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
	}

	if err := syscall.Kill(pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill process group: %w", err)
	}

	select {
	case <-h.done:
		return nil
	case <-time.After(killTimeout):
		return fmt.Errorf("process group %d did not terminate after SIGKILL", h.PID)
	}
}
