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

func waitForState(t *testing.T, m *Manager, a *Agent, want RuntimeState) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for m.Snapshot(a).State != want && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := m.Snapshot(a).State; got != want {
		t.Fatalf("state = %s, want %s", got, want)
	}
}

func TestStartSpawnsRealProcess(t *testing.T) {
	m := NewManager()
	a := &Agent{ID: "sleepy", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() { _ = m.Stop(a) })

	if err := m.Start(a); err != nil {
		t.Fatalf("Start: %v", err)
	}

	snap := m.Snapshot(a)
	if snap.PID == 0 {
		t.Fatal("expected a real PID")
	}
	if snap.State != StateRunning {
		t.Fatalf("state = %s, want %s", snap.State, StateRunning)
	}
	if !processAlive(snap.PID) {
		t.Fatalf("process %d is not alive", snap.PID)
	}
}

func TestStopTerminatesProcess(t *testing.T) {
	m := NewManager()
	a := &Agent{ID: "sleepy", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() { _ = m.Stop(a) })

	if err := m.Start(a); err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := m.Snapshot(a).PID

	if err := m.Stop(a); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	snap := m.Snapshot(a)
	if snap.State != StateStopped {
		t.Fatalf("state = %s, want %s", snap.State, StateStopped)
	}
	if snap.PID != 0 {
		t.Fatalf("PID = %d, want 0 after Stop", snap.PID)
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
	oldPID := m.Snapshot(a).PID

	if err := m.Restart(a); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	snap := m.Snapshot(a)
	if processAlive(oldPID) {
		t.Fatalf("old process %d still alive after restart", oldPID)
	}
	if !processAlive(snap.PID) || snap.PID == oldPID {
		t.Fatalf("new process not healthy: pid=%d oldPID=%d", snap.PID, oldPID)
	}
	if snap.State != StateRunning {
		t.Fatalf("state = %s, want %s", snap.State, StateRunning)
	}
}

func TestMonitorDetectsCrash(t *testing.T) {
	m := NewManager()
	a := &Agent{ID: "boom", Command: "sh", Args: []string{"-c", "exit 1"}}
	t.Cleanup(func() { _ = m.Stop(a) })
	if err := m.Start(a); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForState(t, m, a, StateCrashed)
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

	sa, sb := m.Snapshot(a), m.Snapshot(b)
	if sa.PID == sb.PID {
		t.Fatalf("PIDs collided: %d", sa.PID)
	}
	if !processAlive(sa.PID) || !processAlive(sb.PID) {
		t.Fatalf("not all started processes alive: %d %d", sa.PID, sb.PID)
	}
	if sa.State != StateRunning {
		t.Fatalf("alpha = %s, want %s", sa.State, StateRunning)
	}
	if sb.State != StateRunning {
		t.Fatalf("beta = %s, want %s", sb.State, StateRunning)
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
	alphaPID, betaPID := m.Snapshot(a).PID, m.Snapshot(b).PID

	if err := m.Stop(a); err != nil {
		t.Fatalf("Stop alpha: %v", err)
	}

	if processAlive(alphaPID) {
		t.Fatalf("alpha process %d still alive", alphaPID)
	}
	if !processAlive(betaPID) {
		t.Fatalf("beta process %d died when alpha was stopped", betaPID)
	}
	if got := m.Snapshot(a).State; got != StateStopped {
		t.Fatalf("alpha = %s, want %s", got, StateStopped)
	}
	if got := m.Snapshot(b).State; got != StateRunning {
		t.Fatalf("beta = %s, want %s", got, StateRunning)
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
	oldPID, betaPID := m.Snapshot(a).PID, m.Snapshot(b).PID

	if err := m.Restart(a); err != nil {
		t.Fatalf("Restart alpha: %v", err)
	}

	snap := m.Snapshot(a)
	if processAlive(oldPID) {
		t.Fatalf("old alpha process %d still alive after restart", oldPID)
	}
	if !processAlive(snap.PID) || snap.PID == oldPID {
		t.Fatalf("alpha not healthy after restart: pid=%d oldPID=%d", snap.PID, oldPID)
	}
	if beta := m.Snapshot(b); beta.PID != betaPID {
		t.Fatalf("beta PID changed (%d → %d) while alpha restarted", betaPID, beta.PID)
	} else if beta.State != StateRunning {
		t.Fatalf("beta = %s, want %s", beta.State, StateRunning)
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
	betaPID := m.Snapshot(b).PID

	waitForState(t, m, a, StateCrashed)

	if !processAlive(betaPID) {
		t.Fatalf("beta process %d died when alpha exited naturally", betaPID)
	}
	if got := m.Snapshot(b).State; got != StateRunning {
		t.Fatalf("beta = %s, want %s", got, StateRunning)
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
	pid := m.Snapshot(a).PID

	if err := m.Start(b); err == nil {
		t.Fatal("expected duplicate id to be rejected, got nil error")
	}

	if got, _ := m.Get("dup"); got != a {
		t.Fatalf("registry no longer points at the original agent")
	}
	if !processAlive(pid) {
		t.Fatalf("original process %d died or was never tracked", pid)
	}
	if got := m.Snapshot(a).State; got != StateRunning {
		t.Fatalf("alpha = %s, want %s", got, StateRunning)
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
	if got := m.Snapshot(a).State; got != StateStopped {
		t.Fatalf("state = %s, want %s", got, StateStopped)
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
	pid := m.Snapshot(a).PID

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
	if got := m.Snapshot(a).State; got != StateStopped {
		t.Fatalf("state = %s, want %s", got, StateStopped)
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
	if got := m.Snapshot(a).State; got != StateStopped {
		t.Fatalf("state = %s, want %s", got, StateStopped)
	}
}

func TestRestartAlwaysProducesFreshGeneration(t *testing.T) {
	m := NewManager()
	a := &Agent{ID: "sleepy", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() { _ = m.Stop(a) })

	if err := m.Start(a); err != nil {
		t.Fatalf("Start: %v", err)
	}
	seen := map[int]bool{m.Snapshot(a).PID: true}

	for i := 0; i < 3; i++ {
		if err := m.Restart(a); err != nil {
			t.Fatalf("Restart %d: %v", i, err)
		}
		snap := m.Snapshot(a)
		if seen[snap.PID] {
			t.Fatalf("pid %d reused across generations", snap.PID)
		}
		seen[snap.PID] = true
		if !processAlive(snap.PID) {
			t.Fatalf("generation %d not alive: pid=%d", i+1, snap.PID)
		}
		if snap.State != StateRunning {
			t.Fatalf("state = %s, want %s", snap.State, StateRunning)
		}
	}
}

func TestSessionStableAcrossGenerations(t *testing.T) {
	m := NewManager()
	a := &Agent{ID: "evolve", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() { _ = m.Stop(a) })

	if err := m.Start(a); err != nil {
		t.Fatalf("Start: %v", err)
	}
	s1 := m.Snapshot(a)
	if s1.Generation != 1 {
		t.Fatalf("generation = %d, want 1", s1.Generation)
	}

	if err := m.Restart(a); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	s2 := m.Snapshot(a)
	if s2.AgentID != s1.AgentID || s2.SessionID != s1.SessionID {
		t.Fatalf("identity changed across restart: %+v → %+v", s1, s2)
	}
	if s2.Generation != 2 {
		t.Fatalf("generation = %d, want 2", s2.Generation)
	}
	if s2.PID == s1.PID || s2.PID == 0 {
		t.Fatalf("expected a fresh pid, got %d (old %d)", s2.PID, s1.PID)
	}

	if err := m.Restart(a); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	s3 := m.Snapshot(a)
	if s3.Generation != 3 {
		t.Fatalf("generation = %d, want 3", s3.Generation)
	}
	if s3.SessionID != s1.SessionID {
		t.Fatalf("session id changed across generations")
	}
	if s3.PID == s1.PID || s3.PID == s2.PID {
		t.Fatalf("pid reused across generations: %d", s3.PID)
	}
}

func TestStaleSessionCannotMutateNewState(t *testing.T) {
	m := NewManager()
	a := &Agent{ID: "flip", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() { _ = m.Stop(a) })

	if err := m.Start(a); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Stop(a); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	old := a.session
	if old == nil {
		t.Fatal("expected a first session after Start")
	}

	if err := m.Start(a); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if a.session == old {
		t.Fatal("expected a new session for the new generation")
	}

	m.finish(a, old, nil)
	m.finish(a, old, fmt.Errorf("exit status 1"))

	snap := m.Snapshot(a)
	if snap.Generation != 2 {
		t.Fatalf("generation = %d, want 2", snap.Generation)
	}
	if snap.State != StateRunning {
		t.Fatalf("stale session clobbered the newer generation: state = %s", snap.State)
	}
	if !processAlive(snap.PID) {
		t.Fatalf("new generation process %d is not running", snap.PID)
	}
}

func TestSnapshotIsACopy(t *testing.T) {
	m := NewManager()
	a := &Agent{ID: "copy", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() { _ = m.Stop(a) })

	if err := m.Start(a); err != nil {
		t.Fatalf("Start: %v", err)
	}

	snap := m.Snapshot(a)
	snap.State = StateStopped
	snap.PID = 999999
	snap.Generation = 77

	if got := m.Snapshot(a); got.State != StateRunning {
		t.Fatalf("mutating a snapshot leaked into live state: %s", got.State)
	} else if got.PID == 999999 || got.Generation == 77 {
		t.Fatalf("mutating a snapshot leaked into live metadata")
	}
}
