package driver

import (
	"errors"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestStartSpawnsAndStopTerminates(t *testing.T) {
	d := NewProcessDriver()
	h, err := d.Start(Command{Path: "sleep", Args: []string{"1000"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if h.PID == 0 {
		t.Fatal("expected a real PID")
	}

	done := make(chan ExitResult, 1)
	go func() { done <- d.Wait(h) }()

	if err := d.Stop(h); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	res := <-done
	var exitErr *exec.ExitError
	if !errors.As(res.Err, &exitErr) {
		t.Fatalf("Err = %T, want *exec.ExitError", res.Err)
	}
	if res.Signal != syscall.SIGTERM {
		t.Fatalf("Signal = %v, want SIGTERM", res.Signal)
	}
}

func TestWaitReportsNonZeroExit(t *testing.T) {
	d := NewProcessDriver()
	h, err := d.Start(Command{Path: "sh", Args: []string{"-c", "exit 7"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	res := d.Wait(h)
	if res.Err == nil {
		t.Fatal("expected non-zero exit to be reported as an error")
	}
	if res.ExitCode != 7 {
		t.Fatalf("ExitCode = %d, want 7", res.ExitCode)
	}
	if res.Signal != -1 {
		t.Fatalf("Signal = %v, want -1 for a plain exit", res.Signal)
	}
}

func TestStopIsBoundedWithoutWait(t *testing.T) {
	d := &ProcessDriver{Grace: 100 * time.Millisecond}
	h, err := d.Start(Command{Path: "sleep", Args: []string{"1000"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	started := time.Now()
	err = d.Stop(h)
	elapsed := time.Since(started)

	bound := d.Grace + killTimeout + time.Second
	if elapsed > bound {
		t.Fatalf("Stop took %v, want bounded by ~%v", elapsed, bound)
	}
	if err == nil {
		t.Fatal("expected an error: process was never reaped, so done can never close")
	}
}
