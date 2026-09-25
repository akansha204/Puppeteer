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
	agents map[AgentID]*Agent
}

const stopGrace = 3 * time.Second

func NewManager() *Manager {
	return &Manager{agents: make(map[AgentID]*Agent)}
}

func (m *Manager) Start(a *Agent) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.agents[a.ID]; ok && existing != a {
		return fmt.Errorf("agent %q already exists", a.ID)
	}
	if s := a.session; s != nil && (s.State == StateRunning || s.State == StateStarting) {
		return fmt.Errorf("agent %q is already %s", a.ID, s.State)
	}

	var gen uint64
	if a.session != nil {
		gen = a.session.Generation
	}
	s := &session{ID: SessionID(a.ID), Generation: gen + 1, State: StateStarting}

	cmd := exec.Command(a.Command, a.Args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start agent %q: %w", a.ID, err)
	}

	s.process = cmd.Process
	s.cmd = cmd
	s.PID = cmd.Process.Pid
	s.StartedAt = time.Now()
	s.done = make(chan struct{})
	s.State = StateRunning

	a.session = s
	m.agents[a.ID] = a
	go m.monitor(a, s)

	return nil
}

func (m *Manager) Stop(a *Agent) error {
	m.mu.Lock()
	s := a.session
	if s == nil || s.State != StateRunning {
		m.mu.Unlock()
		return nil
	}
	s.stopping = true
	sd, pid := s.done, s.PID
	m.mu.Unlock()

	pgid := -pid
	if err := syscall.Kill(pgid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("stop agent %q: %w", a.ID, err)
	}

	select {
	case <-sd:
	case <-time.After(stopGrace):
		_ = syscall.Kill(pgid, syscall.SIGKILL)
		<-sd
	}

	return nil
}

func (m *Manager) Restart(a *Agent) error {
	if err := m.Stop(a); err != nil {
		return err
	}
	return m.Start(a)
}

func (m *Manager) Snapshot(a *Agent) SessionSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	s := a.session
	if s == nil {
		return SessionSnapshot{AgentID: a.ID, SessionID: SessionID(a.ID), State: StateIdle}
	}
	return SessionSnapshot{
		AgentID:    a.ID,
		SessionID:  s.ID,
		Generation: s.Generation,
		State:      s.State,
		PID:        s.PID,
		StartedAt:  s.StartedAt,
		ExitedAt:   s.ExitedAt,
	}
}

func (m *Manager) Snapshots() []SessionSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]SessionSnapshot, 0, len(m.agents))
	for id, a := range m.agents {
		s := a.session
		if s == nil {
			out = append(out, SessionSnapshot{AgentID: id, SessionID: SessionID(id), State: StateIdle})
			continue
		}
		out = append(out, SessionSnapshot{
			AgentID:    id,
			SessionID:  s.ID,
			Generation: s.Generation,
			State:      s.State,
			PID:        s.PID,
			StartedAt:  s.StartedAt,
			ExitedAt:   s.ExitedAt,
		})
	}
	return out
}

func (m *Manager) Get(id AgentID) (*Agent, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.agents[id]
	return a, ok
}

func (m *Manager) GetAgents() map[AgentID]*Agent {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make(map[AgentID]*Agent, len(m.agents))
	for id, a := range m.agents {
		out[id] = a
	}
	return out
}

func (m *Manager) monitor(a *Agent, s *session) {
	err := s.cmd.Wait()
	m.finish(a, s, err)
}

func (m *Manager) finish(a *Agent, s *session, waitErr error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if a.session != s {
		return
	}

	switch {
	case s.stopping:
		s.State = StateStopped
	case waitErr != nil:
		s.State = StateCrashed
	default:
		s.State = StateStopped
	}
	s.ExitedAt = time.Now()
	s.PID = 0
	s.process = nil
	s.cmd = nil
	close(s.done)
}
