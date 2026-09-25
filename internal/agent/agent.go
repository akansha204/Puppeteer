package agent

import (
	"time"

	"github.com/akansha204/pony/internal/driver"
)

type AgentID string

type SessionID string

type RuntimeState string

const (
	StateIdle     RuntimeState = "idle"
	StateStarting RuntimeState = "starting"
	StateRunning  RuntimeState = "running"
	StateCrashed  RuntimeState = "crashed"
	StateStopped  RuntimeState = "stopped"
)

type Agent struct {
	ID      AgentID
	Command string
	Args    []string

	session *session
}

type session struct {
	ID         SessionID
	Generation uint64
	State      RuntimeState
	StartedAt  time.Time
	ExitedAt   time.Time

	PID      int
	h        *driver.Handle
	done     chan struct{} //closed by monitor when the process dies
	stopping bool
}

type SessionSnapshot struct {
	AgentID    AgentID
	SessionID  SessionID
	Generation uint64
	State      RuntimeState
	PID        int
	StartedAt  time.Time
	ExitedAt   time.Time
}
