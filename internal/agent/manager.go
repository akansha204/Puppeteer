package agent

import (
	"context"
	"fmt"
	"sync"
	"syscall"
	"time"

	"github.com/akansha204/pony/internal/driver"
)

type Manager struct {
	mu     sync.Mutex
	agents map[AgentID]*agent
	driver driver.Driver
}

const stopSettle = 2 * time.Second

func NewManager(d driver.Driver) *Manager {
	return &Manager{agents: make(map[AgentID]*agent), driver: d}
}

func (m *Manager) Start(spec AgentSpec) (SessionSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if a, ok := m.agents[spec.ID]; ok {
		if s := a.session; s != nil && (s.State == StateRunning || s.State == StateStarting || s.State == StateStopping) {
			return SessionSnapshot{}, fmt.Errorf("agent %q is already %s", spec.ID, s.State)
		}
	}

	var gen uint64
	if a := m.agents[spec.ID]; a != nil && a.session != nil {
		gen = a.session.Generation
	}
	s := &session{ID: SessionID(spec.ID), Generation: gen + 1, State: StateStarting}

	h, err := m.driver.Start(context.Background(), driver.Spec{
		Path: spec.Command,
		Args: spec.Args,
		Cwd:  spec.Cwd,
		Env:  spec.Env,
	})
	if err != nil {
		return SessionSnapshot{}, fmt.Errorf("start agent %q: %w", spec.ID, err)
	}

	s.PID = h.PID
	s.h = h
	s.StartedAt = time.Now()
	s.done = make(chan struct{})
	s.State = StateRunning

	a := m.agents[spec.ID]
	if a == nil {
		a = &agent{spec: spec}
		m.agents[spec.ID] = a
	}
	a.spec = spec
	a.session = s
	go m.monitor(a, s)

	return snapshotOf(a), nil
}

func (m *Manager) Stop(id AgentID) error {
	m.mu.Lock()
	a := m.agents[id]
	if a == nil {
		m.mu.Unlock()
		return fmt.Errorf("no agent %q", id)
	}
	s := a.session
	if s == nil || s.State != StateRunning {
		m.mu.Unlock()
		return nil
	}
	s.stopReq = true
	s.State = StateStopping
	sd, h := s.done, s.h
	m.mu.Unlock()

	if err := m.driver.Stop(context.Background(), h); err != nil {
		return fmt.Errorf("stop agent %q: %w", id, err)
	}

	select {
	case <-sd:
	case <-time.After(stopSettle):
		return fmt.Errorf("stop agent %q: timed out waiting for process to settle", id)
	}

	return nil
}

func (m *Manager) Restart(id AgentID) error {
	m.mu.Lock()
	a := m.agents[id]
	m.mu.Unlock()
	if a == nil {
		return fmt.Errorf("no agent %q", id)
	}
	if err := m.Stop(id); err != nil {
		return err
	}
	_, err := m.Start(a.spec)
	return err
}

func (m *Manager) Get(id AgentID) (SessionSnapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	a := m.agents[id]
	if a == nil {
		return SessionSnapshot{}, false
	}
	return snapshotOf(a), true
}

func (m *Manager) Snapshots() []SessionSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]SessionSnapshot, 0, len(m.agents))
	for _, a := range m.agents {
		out = append(out, snapshotOf(a))
	}
	return out
}

func snapshotOf(a *agent) SessionSnapshot {
	s := a.session
	if s == nil {
		return SessionSnapshot{AgentID: a.spec.ID, SessionID: SessionID(a.spec.ID), State: StateIdle}
	}
	return SessionSnapshot{
		AgentID:    a.spec.ID,
		SessionID:  s.ID,
		Generation: s.Generation,
		State:      s.State,
		PID:        s.PID,
		StartedAt:  s.StartedAt,
		ExitedAt:   s.ExitedAt,
	}
}

func (m *Manager) monitor(a *agent, s *session) {
	res := m.driver.Wait(s.h)
	m.finish(a, s, res)
}

func (m *Manager) finish(a *agent, s *session, res driver.ExitResult) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if a.session != s {
		return
	}

	switch {
	case res.Err == nil:
		s.State = StateStopped
	case s.stopReq && isTerminationSignal(res.Signal):
		s.State = StateStopped
	default:
		s.State = StateCrashed
	}
	s.ExitedAt = time.Now()
	s.PID = 0
	s.h = nil
	close(s.done)
}

// isTerminationSignal reports whether a process died from the signals Stop
// delivers. A crash that merely overlaps with a stop request keeps its crash
// classification instead of being masked as a clean stop.
func isTerminationSignal(sig syscall.Signal) bool {
	return sig == syscall.SIGTERM || sig == syscall.SIGKILL
}
