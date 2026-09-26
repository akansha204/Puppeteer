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

	// creack/pty.Open() hands back the master (we read and write here) and
	// the slave (the actual tty the child runs on).
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
	cmd.Env = spec.Env
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	// Setsid detaches the child from our terminal and makes it a session
	// leader; Setctty makes the slave its controlling terminal. Ctty names
	// the child's fd for that terminal: 0, because the slave is its stdin.
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
	if h.master == nil {
		return 0, fmt.Errorf("process %d is not on a terminal", h.PID)
	}
	return h.master.Write(data)
}

func (d *PTYDriver) Read(h *Handle, p []byte) (int, error) {
	if h.master == nil {
		return 0, fmt.Errorf("process %d is not on a terminal", h.PID)
	}
	return h.master.Read(p)
}

// Resize publishes a new terminal window size to the process, which sees the
// change through SIGWINCH and a fresh termios stty size.
func (d *PTYDriver) Resize(h *Handle, rows, cols uint16) error {
	if h.master == nil {
		return nil
	}
	return pty.Setsize(h.master, &pty.Winsize{Rows: rows, Cols: cols})
}
