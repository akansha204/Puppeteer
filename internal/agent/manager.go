package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type Manager struct {
	mu     sync.Mutex
	agents map[string]*Agent
}

func NewManager() *Manager {
	return &Manager{agents: make(map[string]*Agent)}
}

func (m *Manager) Start(a *Agent) error {
	m.mu.Lock()
	if a.Status == StatusRunning || a.Status == StatusStarting {
		m.mu.Unlock()
		return fmt.Errorf("agent %q is already %s", a.ID, a.Status)
	}

	cmd := exec.Command(a.Command, a.Args...)
	if err := cmd.Start(); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("start agent %q: %w", a.ID, err)
	}

	a.cmd = cmd
	a.process = cmd.Process
	a.PID = cmd.Process.Pid
	a.StartedAt = time.Now()
	a.done = make(chan struct{})
	a.stopping = false
	a.Status = StatusRunning
	m.agents[a.ID] = a

	go m.monitor(a)
	m.mu.Unlock()

	return nil
}

func (m *Manager) Stop(a *Agent) error {
	m.mu.Lock()
	if a.Status != StatusRunning {
		m.mu.Unlock()
		return nil
	}

	a.stopping = true
	process := a.process
	done := a.done
	m.mu.Unlock()

	if err := process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("stop agent %q: %w", a.ID, err)
	}

	<-done

	m.mu.Lock()
	a.PID = 0
	a.process = nil
	a.Status = StatusStopped
	m.mu.Unlock()

	return nil
}

func (m *Manager) Restart(a *Agent) error {
	if err := m.Stop(a); err != nil {
		return err
	}
	return m.Start(a)
}

func (m *Manager) StatusOf(a *Agent) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return a.Status
}

func (m *Manager) GetAgents() map[string]*Agent {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make(map[string]*Agent, len(m.agents))
	for id, a := range m.agents {
		out[id] = a
	}
	return out
}

func (m *Manager) monitor(a *Agent) {
	err := a.cmd.Wait()

	m.mu.Lock()
	switch {
	case a.stopping:
		a.Status = StatusStopped
	case err != nil:
		a.Status = StatusCrashed
	default:
		a.Status = StatusStopped
	}
	a.process = nil
	a.cmd = nil
	close(a.done)
	m.mu.Unlock()
}
