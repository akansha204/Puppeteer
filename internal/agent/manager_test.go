package agent

import (
	"os"
	"syscall"
	"testing"
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
	if a.Status != StatusRunning {
		t.Fatalf("status = %s, want %s", a.Status, StatusRunning)
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

	if a.Status != StatusStopped {
		t.Fatalf("status = %s, want %s", a.Status, StatusStopped)
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
	if a.Status != StatusRunning {
		t.Fatalf("status = %s, want %s", a.Status, StatusRunning)
	}
}
