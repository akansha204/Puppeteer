package driver

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestPTYDriverInteractiveShell(t *testing.T) {
	d := NewPTYDriver()
	h, err := d.Start(context.Background(), Spec{Path: "sh"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(context.Background(), h)
	if h.PID == 0 {
		t.Fatal("expected a real PID")
	}
	if h.master == nil {
		t.Fatal("expected the handle to carry a PTY master")
	}

	output := make(chan string, 1)
	go func() {
		var got strings.Builder
		buf := make([]byte, 256)
		for {
			n, err := d.Read(h, buf)
			if n > 0 {
				got.Write(buf[:n])
				if strings.Contains(got.String(), "hello") {
					output <- got.String()
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	if _, err := d.Write(h, []byte("echo hello\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	select {
	case got := <-output:
		if !strings.Contains(got, "hello") {
			t.Fatalf("terminal output %q does not contain hello", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the shell to echo output")
	}

	if err := d.Resize(h, 30, 90); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	rows, cols, err := pty.Getsize(h.master)
	if err != nil {
		t.Fatalf("Getsize: %v", err)
	}
	if rows != 30 || cols != 90 {
		t.Fatalf("winsize = %dx%d, want 30x90", rows, cols)
	}

	if _, err := d.Write(h, []byte("exit\n")); err != nil {
		t.Fatalf("Write exit: %v", err)
	}
	res := d.Wait(h)
	if res.Err != nil {
		t.Fatalf("expected a clean exit, got: %v", res.Err)
	}
}
