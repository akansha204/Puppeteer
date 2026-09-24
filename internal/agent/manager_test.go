package agent

import (
	"os"
	"syscall"
	"testing"
	"time"
)

func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func TestStartSpawnsRealProcess(t *testing.T) {
	m := NewManager()
	a := &Agent{ID: "sleepy", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() { _ = m.Stop(a) })

	if err := m.Start(a); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if a.PID == 0 {
		t.Fatal("expected a real PID")
	}
	if m.StatusOf(a) != StatusRunning {
		t.Fatalf("status = %s, want %s", m.StatusOf(a), StatusRunning)
	}
	if !processAlive(a.PID) {
		t.Fatalf("process %d is not alive", a.PID)
	}
}

func TestStopTerminatesProcess(t *testing.T) {
	m := NewManager()
	a := &Agent{ID: "sleepy", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() { _ = m.Stop(a) })

	if err := m.Start(a); err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := a.PID

	if err := m.Stop(a); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if m.StatusOf(a) != StatusStopped {
		t.Fatalf("status = %s, want %s", m.StatusOf(a), StatusStopped)
	}
	if a.PID != 0 {
		t.Fatalf("PID = %d, want 0 after Stop", a.PID)
	}
	if processAlive(pid) {
		t.Fatalf("process %d is still alive after Stop", pid)
	}
}

func TestRestartSpawnsFreshProcess(t *testing.T) {
	m := NewManager()
	a := &Agent{ID: "sleepy", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() { _ = m.Stop(a) })

	if err := m.Start(a); err != nil {
		t.Fatalf("Start: %v", err)
	}
	oldPID := a.PID

	if err := m.Restart(a); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	if processAlive(oldPID) {
		t.Fatalf("old process %d still alive after restart", oldPID)
	}
	if !processAlive(a.PID) || a.PID == oldPID {
		t.Fatalf("new process not healthy: pid=%d oldPID=%d", a.PID, oldPID)
	}
	if m.StatusOf(a) != StatusRunning {
		t.Fatalf("status = %s, want %s", m.StatusOf(a), StatusRunning)
	}
}

func TestMonitorDetectsCrash(t *testing.T) {
	m := NewManager()
	a := &Agent{ID: "boom", Command: "sh", Args: []string{"-c", "exit 1"}}
	t.Cleanup(func() { _ = m.Stop(a) })
	if err := m.Start(a); err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for m.StatusOf(a) != StatusCrashed && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if m.StatusOf(a) != StatusCrashed {
		t.Fatalf("status = %s, want %s", m.StatusOf(a), StatusCrashed)
	}
}
