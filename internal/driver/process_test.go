package driver

import (
	"errors"
	"os/exec"
	"testing"
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
