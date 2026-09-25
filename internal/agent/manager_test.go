package agent

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func processAlive(pid int) bool {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	s := string(raw)
	close := strings.LastIndex(s, ")")
	fields := strings.Fields(s[close+1:])
	if len(fields) == 0 {
		return false
	}
	return fields[0] != "Z"
}

func waitForStatus(t *testing.T, m *Manager, a *Agent, want Status) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for m.StatusOf(a) != want && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := m.StatusOf(a); got != want {
		t.Fatalf("status = %s, want %s", got, want)
	}
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
	waitForStatus(t, m, a, StatusCrashed)
}

func TestTwoAgentsRunIndependently(t *testing.T) {
	m := NewManager()
	a := &Agent{ID: "alpha", Command: "sleep", Args: []string{"1000"}}
	b := &Agent{ID: "beta", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() {
		_ = m.Stop(a)
		_ = m.Stop(b)
	})

	if err := m.Start(a); err != nil {
		t.Fatalf("Start alpha: %v", err)
	}
	if err := m.Start(b); err != nil {
		t.Fatalf("Start beta: %v", err)
	}

	if a.PID == b.PID {
		t.Fatalf("PIDs collided: %d", a.PID)
	}
	if !processAlive(a.PID) || !processAlive(b.PID) {
		t.Fatalf("not all started processes alive: %d %d", a.PID, b.PID)
	}
	if got := m.StatusOf(a); got != StatusRunning {
		t.Fatalf("alpha = %s, want %s", got, StatusRunning)
	}
	if got := m.StatusOf(b); got != StatusRunning {
		t.Fatalf("beta = %s, want %s", got, StatusRunning)
	}
}

func TestStopOneAgentDoesNotAffectOther(t *testing.T) {
	m := NewManager()
	a := &Agent{ID: "alpha", Command: "sleep", Args: []string{"1000"}}
	b := &Agent{ID: "beta", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() {
		_ = m.Stop(a)
		_ = m.Stop(b)
	})

	if err := m.Start(a); err != nil {
		t.Fatalf("Start alpha: %v", err)
	}
	if err := m.Start(b); err != nil {
		t.Fatalf("Start beta: %v", err)
	}
	alphaPID, betaPID := a.PID, b.PID

	if err := m.Stop(a); err != nil {
		t.Fatalf("Stop alpha: %v", err)
	}

	if processAlive(alphaPID) {
		t.Fatalf("alpha process %d still alive", alphaPID)
	}
	if !processAlive(betaPID) {
		t.Fatalf("beta process %d died when alpha was stopped", betaPID)
	}
	if got := m.StatusOf(a); got != StatusStopped {
		t.Fatalf("alpha = %s, want %s", got, StatusStopped)
	}
	if got := m.StatusOf(b); got != StatusRunning {
		t.Fatalf("beta = %s, want %s", got, StatusRunning)
	}
}

func TestRestartOneAgentDoesNotAffectOther(t *testing.T) {
	m := NewManager()
	a := &Agent{ID: "alpha", Command: "sleep", Args: []string{"1000"}}
	b := &Agent{ID: "beta", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() {
		_ = m.Stop(a)
		_ = m.Stop(b)
	})

	if err := m.Start(a); err != nil {
		t.Fatalf("Start alpha: %v", err)
	}
	if err := m.Start(b); err != nil {
		t.Fatalf("Start beta: %v", err)
	}
	oldPID, betaPID := a.PID, b.PID

	if err := m.Restart(a); err != nil {
		t.Fatalf("Restart alpha: %v", err)
	}

	if processAlive(oldPID) {
		t.Fatalf("old alpha process %d still alive after restart", oldPID)
	}
	if !processAlive(a.PID) || a.PID == oldPID {
		t.Fatalf("alpha not healthy after restart: pid=%d oldPID=%d", a.PID, oldPID)
	}
	if b.PID != betaPID {
		t.Fatalf("beta PID changed (%d → %d) while alpha restarted", betaPID, b.PID)
	}
	if got := m.StatusOf(b); got != StatusRunning {
		t.Fatalf("beta = %s, want %s", got, StatusRunning)
	}
}

func TestNaturalExitDoesNotAffectOther(t *testing.T) {
	m := NewManager()
	a := &Agent{ID: "alpha", Command: "sh", Args: []string{"-c", "exit 1"}}
	b := &Agent{ID: "beta", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() {
		_ = m.Stop(a)
		_ = m.Stop(b)
	})

	if err := m.Start(a); err != nil {
		t.Fatalf("Start alpha: %v", err)
	}
	if err := m.Start(b); err != nil {
		t.Fatalf("Start beta: %v", err)
	}
	betaPID := b.PID

	waitForStatus(t, m, a, StatusCrashed)

	if !processAlive(betaPID) {
		t.Fatalf("beta process %d died when alpha exited naturally", betaPID)
	}
	if got := m.StatusOf(b); got != StatusRunning {
		t.Fatalf("beta = %s, want %s", got, StatusRunning)
	}
}

func TestDuplicateIDRejected(t *testing.T) {
	m := NewManager()
	a := &Agent{ID: "dup", Command: "sleep", Args: []string{"1000"}}
	b := &Agent{ID: "dup", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() {
		_ = m.Stop(a)
		_ = m.Stop(b)
	})

	if err := m.Start(a); err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := a.PID

	if err := m.Start(b); err == nil {
		t.Fatal("expected duplicate id to be rejected, got nil error")
	}

	if got, _ := m.Get("dup"); got != a {
		t.Fatalf("registry no longer points at the original agent")
	}
	if !processAlive(pid) {
		t.Fatalf("original process %d died or was never tracked", pid)
	}
	if got := m.StatusOf(a); got != StatusRunning {
		t.Fatalf("alpha = %s, want %s", got, StatusRunning)
	}
}

func TestStopKillsChildProcesses(t *testing.T) {
	m := NewManager()
	childPidFile := filepath.Join(t.TempDir(), "child.pid")
	sh := fmt.Sprintf("sleep 1000 & echo $! > %s; wait", childPidFile)
	a := &Agent{ID: "parent", Command: "sh", Args: []string{"-c", sh}}
	t.Cleanup(func() { _ = m.Stop(a) })

	if err := m.Start(a); err != nil {
		t.Fatalf("Start: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	raw := ""
	for {
		b, err := os.ReadFile(childPidFile)
		if err == nil {
			raw = strings.TrimSpace(string(b))
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("child pid file never appeared: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	childPID, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("bad child pid %q: %v", raw, err)
	}
	if !processAlive(childPID) {
		t.Fatalf("child %d not running before stop", childPID)
	}

	if err := m.Stop(a); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if processAlive(childPID) {
		t.Fatalf("child process %d survived Stop", childPID)
	}
	if m.StatusOf(a) != StatusStopped {
		t.Fatalf("status = %s, want %s", m.StatusOf(a), StatusStopped)
	}
}

func TestStickyProcessHelper(t *testing.T) {
	if os.Getenv("GO_STICKY_HELPER_PROC") != "1" {
		return
	}
	signal.Ignore(syscall.SIGTERM)
	if err := os.WriteFile(os.Getenv("GO_STICKY_HELPER_MARKER"), []byte("ready"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {}
}

func TestStopKillsProcessIgnoringSigterm(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ready")
	os.Setenv("GO_STICKY_HELPER_PROC", "1")
	os.Setenv("GO_STICKY_HELPER_MARKER", marker)
	t.Cleanup(func() {
		os.Unsetenv("GO_STICKY_HELPER_PROC")
		os.Unsetenv("GO_STICKY_HELPER_MARKER")
	})

	m := NewManager()
	a := &Agent{ID: "sticky", Command: os.Args[0], Args: []string{"-test.run", "^TestStickyProcessHelper$"}}
	t.Cleanup(func() { _ = m.Stop(a) })

	if err := m.Start(a); err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := a.PID

	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("sticky helper never reported ready")
		}
		time.Sleep(10 * time.Millisecond)
	}

	started := time.Now()
	if err := m.Stop(a); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	elapsed := time.Since(started)

	if elapsed < 2*time.Second {
		t.Fatalf("process died on SIGTERM, but grace period should have forced SIGKILL (elapsed %v)", elapsed)
	}
	if processAlive(pid) {
		t.Fatalf("process group leader %d survived Stop", pid)
	}
	if m.StatusOf(a) != StatusStopped {
		t.Fatalf("status = %s, want %s", m.StatusOf(a), StatusStopped)
	}
}

func TestStopTwiceIsSafe(t *testing.T) {
	m := NewManager()
	a := &Agent{ID: "sleepy", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() { _ = m.Stop(a) })

	if err := m.Start(a); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Stop(a); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := m.Stop(a); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if m.StatusOf(a) != StatusStopped {
		t.Fatalf("status = %s, want %s", m.StatusOf(a), StatusStopped)
	}
}

func TestRestartAlwaysProducesFreshGeneration(t *testing.T) {
	m := NewManager()
	a := &Agent{ID: "sleepy", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() { _ = m.Stop(a) })

	if err := m.Start(a); err != nil {
		t.Fatalf("Start: %v", err)
	}
	seen := map[int]bool{a.PID: true}

	for i := 0; i < 3; i++ {
		if err := m.Restart(a); err != nil {
			t.Fatalf("Restart %d: %v", i, err)
		}
		if seen[a.PID] {
			t.Fatalf("pid %d reused across generations", a.PID)
		}
		seen[a.PID] = true
		if !processAlive(a.PID) {
			t.Fatalf("generation %d not alive: pid=%d", i+1, a.PID)
		}
		if m.StatusOf(a) != StatusRunning {
			t.Fatalf("status = %s, want %s", m.StatusOf(a), StatusRunning)
		}
	}
}
