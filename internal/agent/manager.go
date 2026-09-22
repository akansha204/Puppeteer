package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

type Manager struct {
	agents map[string]*Agent
}

func NewManager() *Manager {
	return &Manager{agents: make(map[string]*Agent)}
}

func (m *Manager) Start(a *Agent) error {
	if a.Status == StatusRunning || a.Status == StatusStarting {
		return fmt.Errorf("agent %q is already %s", a.ID, a.Status)
	}

	cmd := exec.Command(a.Command, a.Args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start agent %q: %w", a.ID, err)
	}

	a.process = cmd.Process
	a.PID = cmd.Process.Pid
	a.StartedAt = time.Now()
	a.Status = StatusRunning

	return nil
}

func (m *Manager) Stop(a *Agent) error {
	if a.Status != StatusRunning {
		return nil
	}

	if err := a.process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("stop agent %q: %w", a.ID, err)
	}

	_, err := a.process.Wait()
	if err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("stop agent %q: %w", a.ID, err)
	}

	a.PID = 0
	a.process = nil
	a.Status = StatusStopped
	return nil
}

func (m *Manager) Restart(a *Agent) error {
	if err := m.Stop(a); err != nil {
		return err
	}
	return m.Start(a)
}

func (m *Manager) GetAgents() map[string]*Agent {
	return m.agents
}
