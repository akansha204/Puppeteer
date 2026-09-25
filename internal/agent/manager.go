package agent

import (
	"fmt"
	"sync"
	"time"

	"github.com/akansha204/pony/internal/driver"
)

type Manager struct {
	mu     sync.Mutex
	agents map[AgentID]*Agent
	driver driver.Driver
}

const stopSettle = 2 * time.Second

func NewManager(d driver.Driver) *Manager {
	return &Manager{agents: make(map[AgentID]*Agent), driver: d}
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

	h, err := m.driver.Start(driver.Command{Path: a.Command, Args: a.Args})
	if err != nil {
		return fmt.Errorf("start agent %q: %w", a.ID, err)
	}

	s.PID = h.PID
	s.h = h
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
	sd, h := s.done, s.h
	m.mu.Unlock()

	if err := m.driver.Stop(h); err != nil {
		return fmt.Errorf("stop agent %q: %w", a.ID, err)
	}

	select {
	case <-sd:
	case <-time.After(stopSettle):
		return fmt.Errorf("stop agent %q: timed out waiting for process to settle", a.ID)
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
	res := m.driver.Wait(s.h)
	m.finish(a, s, res.ExitErr)
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
	s.h = nil
	close(s.done)
}
