package driver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStartSpawnsAndStopTerminates(t *testing.T) {
	d := NewProcessDriver()
	h, err := d.Start(context.Background(), Spec{Path: "sleep", Args: []string{"1000"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if h.PID == 0 {
		t.Fatal("expected a real PID")
	}

	done := make(chan ExitResult, 1)
	go func() { done <- d.Wait(h) }()

	if err := d.Stop(context.Background(), h); err != nil {
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
	h, err := d.Start(context.Background(), Spec{Path: "sh", Args: []string{"-c", "exit 7"}})
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

func TestWriteFeedsStdin(t *testing.T) {
	d := NewProcessDriver()
	h, err := d.Start(context.Background(), Spec{Path: "sh", Args: []string{"-c", `read -r line; [ "$line" = "ping" ]`}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	n, err := d.Write(h, []byte("ping\n"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len("ping\n") {
		t.Fatalf("wrote %d bytes, want %d", n, len("ping\n"))
	}

	res := d.Wait(h)
	if res.Err != nil {
		t.Fatalf("process should exit 0 after reading its line: %v", res.Err)
	}
}

func TestStartAppliesCwdAndEnv(t *testing.T) {
	d := NewProcessDriver()
	dir := t.TempDir()
	h, err := d.Start(context.Background(), Spec{
		Path: "sleep",
		Args: []string{"1000"},
		Cwd:  dir,
		Env:  []string{"PATH=" + os.Getenv("PATH"), "PONY_VAL=1"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		done := make(chan struct{})
		go func() {
			d.Wait(h)
			close(done)
		}()
		_ = d.Stop(context.Background(), h)
		<-done
	})

	got, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", h.PID))
	if err != nil {
		t.Fatalf("read cwd: %v", err)
	}
	if got != dir {
		t.Fatalf("cwd = %q, want %q", got, dir)
	}

	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", h.PID))
	if err != nil {
		t.Fatalf("read environ: %v", err)
	}
	if !strings.Contains(string(data), "PONY_VAL=1") {
		t.Fatal("PONY_VAL=1 not present in the process environment")
	}
}

func TestProcessDriverReadOutput(t *testing.T) {
	d := NewProcessDriver()
	h, err := d.Start(context.Background(), Spec{Path: "sh", Args: []string{"-c", "echo hi"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	var got strings.Builder
	buf := make([]byte, 64)
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(got.String(), "hi") && time.Now().Before(deadline) {
		n, err := d.Read(h, buf)
		if err != nil {
			break
		}
		if n > 0 {
			got.Write(buf[:n])
		}
	}
	if !strings.Contains(got.String(), "hi") {
		t.Fatalf("output %q does not contain hi", got.String())
	}

	res := d.Wait(h)
	if res.Err != nil {
		t.Fatalf("expected clean exit, got: %v", res.Err)
	}
}

func TestStartFailureDoesNotLeakDescriptors(t *testing.T) {
	d := NewProcessDriver()
	countFDs := func() int {
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Fatalf("read /proc/self/fd: %v", err)
		}
		return len(entries)
	}

	before := countFDs()
	for i := 0; i < 100; i++ {
		if h, err := d.Start(context.Background(), Spec{Path: "definitely-no-such-binary-pony-test"}); err == nil {
			_ = h
			t.Fatal("expected Start to fail for a missing binary")
		}
	}
	after := countFDs()

	const slack = 4
	if after > before+slack {
		t.Fatalf("failed starts leaked descriptors: %d -> %d", before, after)
	}
}

func TestStopIsBoundedWithoutWait(t *testing.T) {
	d := &ProcessDriver{Grace: 100 * time.Millisecond}
	h, err := d.Start(context.Background(), Spec{Path: "sleep", Args: []string{"1000"}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	started := time.Now()
	err = d.Stop(context.Background(), h)
	elapsed := time.Since(started)

	bound := d.Grace + killTimeout + time.Second
	if elapsed > bound {
		t.Fatalf("Stop took %v, want bounded by ~%v", elapsed, bound)
	}
	if err == nil {
		t.Fatal("expected an error: process was never reaped, so done can never close")
	}
}
