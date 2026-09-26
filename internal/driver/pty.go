package driver

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

// defaultWinsize is the terminal size applied to a freshly started PTY.
var defaultWinsize = &pty.Winsize{Rows: 24, Cols: 80}

type PTYDriver struct {
	*ProcessDriver
}

func NewPTYDriver() *PTYDriver {
	return &PTYDriver{ProcessDriver: NewProcessDriver()}
}

func (d *PTYDriver) Start(ctx context.Context, spec Spec) (*Handle, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// creack/pty.Open returns the master (we read/write here) and the slave
	// (the tty the child runs on).
	master, slave, err := pty.Open()
	if err != nil {
		return nil, err
	}
	if err := pty.Setsize(master, defaultWinsize); err != nil {
		master.Close()
		slave.Close()
		return nil, err
	}

	cmd := exec.CommandContext(ctx, spec.Path, spec.Args...)
	cmd.Dir = spec.Cwd
	cmd.Env = mergedEnv(spec.Env)
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	// Setsid: new session/group; Setctty: slave is its controlling terminal.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    0,
	}

	startErr := cmd.Start()
	slave.Close() // only the child keeps the slave open after this
	if startErr != nil {
		master.Close()
		return nil, startErr
	}

	return &Handle{
		PID:    cmd.Process.Pid,
		done:   make(chan struct{}),
		proc:   cmd.Process,
		cmd:    cmd,
		master: master,
	}, nil
}

func (d *PTYDriver) Write(h *Handle, data []byte) (int, error) {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()

	// Held across the write so Wait cannot close the master mid-write.
	h.stateMu.RLock()
	defer h.stateMu.RUnlock()

	if h.master == nil {
		return 0, fmt.Errorf("%w: process %d is not on a terminal", ErrClosed, h.PID)
	}
	return h.master.Write(data)
}

func (d *PTYDriver) Read(h *Handle, p []byte) (int, error) {
	h.readMu.Lock()
	defer h.readMu.Unlock()

	// readMu is separate from writeMu, so a read and a write stay full-duplex.
	h.stateMu.RLock()
	defer h.stateMu.RUnlock()

	if h.master == nil {
		return 0, fmt.Errorf("%w: process %d is not on a terminal", ErrClosed, h.PID)
	}
	return h.master.Read(p)
}

// Resize publishes a new window size to the process (SIGWINCH). Held under
// the state lock so it never resizes a master Wait is closing.
func (d *PTYDriver) Resize(h *Handle, rows, cols uint16) error {
	if rows == 0 || cols == 0 {
		return fmt.Errorf("terminal rows and columns must be > 0")
	}

	// Held across Setsize so Wait cannot close the master mid-call.
	h.stateMu.RLock()
	defer h.stateMu.RUnlock()

	if h.master == nil {
		return fmt.Errorf("%w: process %d has no PTY", ErrClosed, h.PID)
	}

	return pty.Setsize(h.master, &pty.Winsize{
		Rows: rows,
		Cols: cols,
	})
}
