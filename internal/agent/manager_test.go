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

	"github.com/akansha204/pony/internal/driver"
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

func waitForState(t *testing.T, m *Manager, id AgentID, want RuntimeState) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		snap, ok := m.Get(id)
		if ok && snap.State == want {
			return
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	snap, ok := m.Get(id)
	if !ok {
		t.Fatalf("agent %s missing, want state %s", id, want)
	}
	if snap.State != want {
		t.Fatalf("state = %s, want %s", snap.State, want)
	}
}

func TestStartSpawnsRealProcess(t *testing.T) {
	m := NewManager(driver.NewProcessDriver())
	spec := AgentSpec{ID: "sleepy", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() { _ = m.Stop(spec.ID) })

	snap, err := m.Start(spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
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
	m := NewManager(driver.NewProcessDriver())
	spec := AgentSpec{ID: "sleepy", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() { _ = m.Stop(spec.ID) })

	snap, err := m.Start(spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := snap.PID

	if err := m.Stop(spec.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	got, ok := m.Get(spec.ID)
	if !ok {
		t.Fatal("agent missing after Stop")
	}
	if got.State != StateStopped {
		t.Fatalf("state = %s, want %s", got.State, StateStopped)
	}
	if got.PID != 0 {
		t.Fatalf("PID = %d, want 0 after Stop", got.PID)
	}
	if processAlive(pid) {
		t.Fatalf("process %d is still alive after Stop", pid)
	}
}

func TestRestartSpawnsFreshProcess(t *testing.T) {
	m := NewManager(driver.NewProcessDriver())
	spec := AgentSpec{ID: "sleepy", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() { _ = m.Stop(spec.ID) })

	snap, err := m.Start(spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	oldPID := snap.PID

	if err := m.Restart(spec.ID); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	got, ok := m.Get(spec.ID)
	if !ok {
		t.Fatal("agent missing after Restart")
	}
	if processAlive(oldPID) {
		t.Fatalf("old process %d still alive after restart", oldPID)
	}
	if got.PID == 0 || !processAlive(got.PID) {
		t.Fatalf("new process not running: pid=%d", got.PID)
	}
	if got.State != StateRunning {
		t.Fatalf("state = %s, want %s", got.State, StateRunning)
	}
}

func TestMonitorDetectsCrash(t *testing.T) {
	m := NewManager(driver.NewProcessDriver())
	spec := AgentSpec{ID: "boom", Command: "sh", Args: []string{"-c", "exit 1"}}
	t.Cleanup(func() { _ = m.Stop(spec.ID) })
	if _, err := m.Start(spec); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForState(t, m, spec.ID, StateCrashed)
}

func TestTwoAgentsRunIndependently(t *testing.T) {
	m := NewManager(driver.NewProcessDriver())
	alpha := AgentSpec{ID: "alpha", Command: "sleep", Args: []string{"1000"}}
	beta := AgentSpec{ID: "beta", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() {
		_ = m.Stop(alpha.ID)
		_ = m.Stop(beta.ID)
	})

	sa, err := m.Start(alpha)
	if err != nil {
		t.Fatalf("Start alpha: %v", err)
	}
	sb, err := m.Start(beta)
	if err != nil {
		t.Fatalf("Start beta: %v", err)
	}

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
	m := NewManager(driver.NewProcessDriver())
	alpha := AgentSpec{ID: "alpha", Command: "sleep", Args: []string{"1000"}}
	beta := AgentSpec{ID: "beta", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() {
		_ = m.Stop(alpha.ID)
		_ = m.Stop(beta.ID)
	})

	sa, err := m.Start(alpha)
	if err != nil {
		t.Fatalf("Start alpha: %v", err)
	}
	sb, err := m.Start(beta)
	if err != nil {
		t.Fatalf("Start beta: %v", err)
	}
	alphaPID, betaPID := sa.PID, sb.PID

	if err := m.Stop(alpha.ID); err != nil {
		t.Fatalf("Stop alpha: %v", err)
	}

	if processAlive(alphaPID) {
		t.Fatalf("alpha process %d still alive", alphaPID)
	}
	if !processAlive(betaPID) {
		t.Fatalf("beta process %d died when alpha was stopped", betaPID)
	}
	if got, _ := m.Get(alpha.ID); got.State != StateStopped {
		t.Fatalf("alpha = %s, want %s", got.State, StateStopped)
	}
	if got, _ := m.Get(beta.ID); got.State != StateRunning {
		t.Fatalf("beta = %s, want %s", got.State, StateRunning)
	}
}

func TestRestartOneAgentDoesNotAffectOther(t *testing.T) {
	m := NewManager(driver.NewProcessDriver())
	alpha := AgentSpec{ID: "alpha", Command: "sleep", Args: []string{"1000"}}
	beta := AgentSpec{ID: "beta", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() {
		_ = m.Stop(alpha.ID)
		_ = m.Stop(beta.ID)
	})

	sa, err := m.Start(alpha)
	if err != nil {
		t.Fatalf("Start alpha: %v", err)
	}
	sb, err := m.Start(beta)
	if err != nil {
		t.Fatalf("Start beta: %v", err)
	}
	oldPID, betaPID := sa.PID, sb.PID

	if err := m.Restart(alpha.ID); err != nil {
		t.Fatalf("Restart alpha: %v", err)
	}

	got, _ := m.Get(alpha.ID)
	if processAlive(oldPID) {
		t.Fatalf("old alpha process %d still alive after restart", oldPID)
	}
	if got.PID == 0 || !processAlive(got.PID) {
		t.Fatalf("alpha not running after restart: pid=%d", got.PID)
	}
	if gb, _ := m.Get(beta.ID); gb.PID != betaPID {
		t.Fatalf("beta PID changed (%d → %d) while alpha restarted", betaPID, gb.PID)
	} else if gb.State != StateRunning {
		t.Fatalf("beta = %s, want %s", gb.State, StateRunning)
	}
}

func TestNaturalExitDoesNotAffectOther(t *testing.T) {
	m := NewManager(driver.NewProcessDriver())
	alpha := AgentSpec{ID: "alpha", Command: "sh", Args: []string{"-c", "exit 1"}}
	beta := AgentSpec{ID: "beta", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() {
		_ = m.Stop(alpha.ID)
		_ = m.Stop(beta.ID)
	})

	if _, err := m.Start(alpha); err != nil {
		t.Fatalf("Start alpha: %v", err)
	}
	sb, err := m.Start(beta)
	if err != nil {
		t.Fatalf("Start beta: %v", err)
	}
	betaPID := sb.PID

	waitForState(t, m, alpha.ID, StateCrashed)

	if !processAlive(betaPID) {
		t.Fatalf("beta process %d died when alpha exited naturally", betaPID)
	}
	if got, _ := m.Get(beta.ID); got.State != StateRunning {
		t.Fatalf("beta = %s, want %s", got.State, StateRunning)
	}
}

func TestDuplicateIDRejected(t *testing.T) {
	m := NewManager(driver.NewProcessDriver())
	spec := AgentSpec{ID: "dup", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() { _ = m.Stop(spec.ID) })

	snap, err := m.Start(spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := m.Start(spec); err == nil {
		t.Fatal("expected duplicate id to be rejected, got nil error")
	}

	if _, ok := m.Get(spec.ID); !ok {
		t.Fatal("registry no longer tracks the original agent")
	}
	if !processAlive(snap.PID) {
		t.Fatalf("original process %d died or was never tracked", snap.PID)
	}
	if got, _ := m.Get(spec.ID); got.State != StateRunning {
		t.Fatalf("agent = %s, want %s", got.State, StateRunning)
	}
}

func TestStopKillsChildProcesses(t *testing.T) {
	m := NewManager(driver.NewProcessDriver())
	childPidFile := filepath.Join(t.TempDir(), "child.pid")
	sh := fmt.Sprintf("sleep 1000 & echo $! > %s; wait", childPidFile)
	spec := AgentSpec{ID: "parent", Command: "sh", Args: []string{"-c", sh}}
	t.Cleanup(func() { _ = m.Stop(spec.ID) })

	if _, err := m.Start(spec); err != nil {
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

	if err := m.Stop(spec.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if processAlive(childPID) {
		t.Fatalf("child process %d survived Stop", childPID)
	}
	if got, _ := m.Get(spec.ID); got.State != StateStopped {
		t.Fatalf("state = %s, want %s", got.State, StateStopped)
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

	m := NewManager(driver.NewProcessDriver())
	spec := AgentSpec{ID: "sticky", Command: os.Args[0], Args: []string{"-test.run", "^TestStickyProcessHelper$"}}
	t.Cleanup(func() { _ = m.Stop(spec.ID) })

	snap, err := m.Start(spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := snap.PID

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
	if err := m.Stop(spec.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	elapsed := time.Since(started)

	if elapsed < 2*time.Second {
		t.Fatalf("process died on SIGTERM, but grace period should have forced SIGKILL (elapsed %v)", elapsed)
	}
	if processAlive(pid) {
		t.Fatalf("process group leader %d survived Stop", pid)
	}
	if got, _ := m.Get(spec.ID); got.State != StateStopped {
		t.Fatalf("state = %s, want %s", got.State, StateStopped)
	}
}

func TestStopTwiceIsSafe(t *testing.T) {
	m := NewManager(driver.NewProcessDriver())
	spec := AgentSpec{ID: "sleepy", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() { _ = m.Stop(spec.ID) })

	if _, err := m.Start(spec); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Stop(spec.ID); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := m.Stop(spec.ID); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if got, _ := m.Get(spec.ID); got.State != StateStopped {
		t.Fatalf("state = %s, want %s", got.State, StateStopped)
	}
}

func TestRestartAlwaysProducesFreshGeneration(t *testing.T) {
	m := NewManager(driver.NewProcessDriver())
	spec := AgentSpec{ID: "sleepy", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() { _ = m.Stop(spec.ID) })

	snap, err := m.Start(spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	wantGen := snap.Generation

	for i := 0; i < 3; i++ {
		if err := m.Restart(spec.ID); err != nil {
			t.Fatalf("Restart %d: %v", i, err)
		}
		snap, _ := m.Get(spec.ID)
		if snap.Generation != wantGen+1 {
			t.Fatalf("restart %d did not bump generation: got %d, want %d", i+1, snap.Generation, wantGen+1)
		}
		wantGen = snap.Generation
		if snap.PID == 0 {
			t.Fatalf("generation %d has no PID", wantGen)
		}
		if !processAlive(snap.PID) {
			t.Fatalf("generation %d not alive: pid=%d", wantGen, snap.PID)
		}
		if snap.State != StateRunning {
			t.Fatalf("state = %s, want %s", snap.State, StateRunning)
		}
	}
}

func TestSessionStableAcrossGenerations(t *testing.T) {
	m := NewManager(driver.NewProcessDriver())
	spec := AgentSpec{ID: "evolve", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() { _ = m.Stop(spec.ID) })

	s1, err := m.Start(spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if s1.Generation != 1 {
		t.Fatalf("generation = %d, want 1", s1.Generation)
	}

	if err := m.Restart(spec.ID); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	s2, _ := m.Get(spec.ID)
	if s2.AgentID != s1.AgentID || s2.SessionID != s1.SessionID {
		t.Fatalf("identity changed across restart: %+v → %+v", s1, s2)
	}
	if s2.Generation != 2 {
		t.Fatalf("generation = %d, want 2", s2.Generation)
	}
	if s2.PID == 0 || !processAlive(s2.PID) {
		t.Fatalf("generation 2 not running: pid=%d", s2.PID)
	}

	if err := m.Restart(spec.ID); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	s3, _ := m.Get(spec.ID)
	if s3.Generation != 3 {
		t.Fatalf("generation = %d, want 3", s3.Generation)
	}
	if s3.SessionID != s1.SessionID {
		t.Fatalf("session id changed across generations")
	}
	if s3.PID == 0 || !processAlive(s3.PID) {
		t.Fatalf("generation 3 not running: pid=%d", s3.PID)
	}
}

func TestStaleSessionCannotMutateNewState(t *testing.T) {
	m := NewManager(driver.NewProcessDriver())
	spec := AgentSpec{ID: "flip", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() { _ = m.Stop(spec.ID) })

	if _, err := m.Start(spec); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Stop(spec.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	flip := m.agents[spec.ID]
	if flip == nil {
		t.Fatal("expected the agent in the registry")
	}
	old := flip.session
	if old == nil {
		t.Fatal("expected a first session after Start")
	}

	if _, err := m.Start(spec); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if flip.session == old {
		t.Fatal("expected a new session for the new generation")
	}

	m.finish(flip, old, nil)
	m.finish(flip, old, fmt.Errorf("exit status 1"))

	snap, ok := m.Get(spec.ID)
	if !ok {
		t.Fatal("agent missing")
	}
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
	m := NewManager(driver.NewProcessDriver())
	spec := AgentSpec{ID: "copy", Command: "sleep", Args: []string{"1000"}}
	t.Cleanup(func() { _ = m.Stop(spec.ID) })

	if _, err := m.Start(spec); err != nil {
		t.Fatalf("Start: %v", err)
	}

	snap, ok := m.Get(spec.ID)
	if !ok {
		t.Fatal("agent missing")
	}
	snap.State = StateStopped
	snap.PID = 999999
	snap.Generation = 77

	got, _ := m.Get(spec.ID)
	if got.State != StateRunning {
		t.Fatalf("mutating a snapshot leaked into live state: %s", got.State)
	} else if got.PID == 999999 || got.Generation == 77 {
		t.Fatalf("mutating a snapshot leaked into live metadata")
	}
}
