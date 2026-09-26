package driver

import (
	"errors"
	"os/exec"
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

	done := make(chan Result, 1)
	go func() { done <- d.Wait(h) }()

	if err := d.Stop(h); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	res := <-done
	var exitErr *exec.ExitError
	if !errors.As(res.ExitErr, &exitErr) {
		t.Fatalf("ExitErr = %T, want *exec.ExitError", res.ExitErr)
	}
}

func TestWaitReportsNonZeroExit(t *testing.T) {
	d := NewProcessDriver()
	h, err := d.Start(Command{Path: "sh", Args: []string{"-c", "exit 7"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	res := d.Wait(h)
	if res.ExitErr == nil {
		t.Fatal("expected non-zero exit to be reported as an error")
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
