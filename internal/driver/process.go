package driver

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const defaultGrace = 3 * time.Second

type Command struct {
	Path string
	Args []string
}

type Result struct {
	ExitErr error
}

type Handle struct {
	PID int

	done chan struct{} //closed by Wait when the process dies
	proc *os.Process
	cmd  *exec.Cmd
}

type Driver interface {
	Start(Command) (*Handle, error)
	Wait(h *Handle) Result
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

func (d *ProcessDriver) Wait(h *Handle) Result {
	err := h.cmd.Wait()
	h.proc = nil
	h.cmd = nil
	close(h.done)
	return Result{ExitErr: err}
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
	case <-time.After(grace):
		_ = syscall.Kill(pgid, syscall.SIGKILL)
		<-h.done
	}
	return nil
}
