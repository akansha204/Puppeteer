package agent

import (
	"os"
	"os/exec"
	"time"
)

type Status string

const (
	StatusIdle     Status = "idle"
	StatusStarting Status = "starting"
	StatusRunning  Status = "running"
	StatusCrashed  Status = "crashed"
	StatusStopped  Status = "stopped"
)

type Agent struct {
	ID      string
	Command string
	Args    []string
	PID     int
	Status  Status

	StartedAt time.Time
	process   *os.Process
	cmd       *exec.Cmd
	done      chan struct{} //closed by monitor when process dies
	stopping  bool
}
