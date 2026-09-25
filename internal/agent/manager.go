package agent

import (
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type Manager struct {
	mu     sync.Mutex
	agents map[string]*Agent
}

const stopGrace = 3 * time.Second

func NewManager() *Manager {
	return &Manager{agents: make(map[string]*Agent)}
}

func (m *Manager) Start(a *Agent) error {
	m.mu.Lock()
	if existing, ok := m.agents[a.ID]; ok && existing != a {
		m.mu.Unlock()
		return fmt.Errorf("agent %q already exists", a.ID)
	}
	if a.Status == StatusRunning || a.Status == StatusStarting {
		m.mu.Unlock()
		return fmt.Errorf("agent %q is already %s", a.ID, a.Status)
	}

	cmd := exec.Command(a.Command, a.Args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
	pid := a.PID
	done := a.done
	m.mu.Unlock()

	pgid := -pid
	if err := syscall.Kill(pgid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("stop agent %q: %w", a.ID, err)
	}

	select {
	case <-done:
	case <-time.After(stopGrace):
		_ = syscall.Kill(pgid, syscall.SIGKILL)
		<-done
	}

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

func (m *Manager) Get(id string) (*Agent, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.agents[id]
	return a, ok
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
