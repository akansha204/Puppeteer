package driver

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// Regression for the full-duplex requirement: a goroutine parked in a
// blocking read must never stop a concurrent write from reaching the agent.
// The old single-mutex handle deadlocked this exact shape (reader waits for
// output, agent waits for input, writer waits for the reader).
func TestBlockingReadDoesNotBlockWrite(t *testing.T) {
	for _, tc := range []struct {
		name string
		d    Driver
	}{
		{"pty", NewPTYDriver()},
		{"pipe", NewProcessDriver()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, err := tc.d.Start(context.Background(), Spec{Path: "sh"})
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			t.Cleanup(func() {
				stopped := make(chan error, 1)
				go func() { stopped <- tc.d.Stop(context.Background(), h) }()
				tc.d.Wait(h) // close done so Stop settles the moment the process dies
				<-stopped
			})

			got := make(chan struct{})
			go func() {
				defer close(got)
				var seen strings.Builder
				buf := make([]byte, 256)
				for {
					n, err := tc.d.Read(h, buf)
					if n > 0 {
						seen.Write(buf[:n])
						if strings.Contains(seen.String(), "hello") {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}()
			// Let the reader be the one to get blocked first.
			time.Sleep(100 * time.Millisecond)

			wrote := make(chan error, 1)
			go func() {
				_, err := tc.d.Write(h, []byte("echo hello\n"))
				wrote <- err
			}()

			select {
			case err := <-wrote:
				if err != nil {
					t.Fatalf("Write: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("deadlock: blocking Read held a lock Write needed")
			}
			<-got
		})
	}
}

func TestReadTimeoutDrainsThenGoesQuiet(t *testing.T) {
	d := NewPTYDriver()
	h, err := d.Start(context.Background(), Spec{Path: "sh"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(context.Background(), h)

	if _, err := d.Write(h, []byte("echo hi\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	buf := make([]byte, 256)
	var got strings.Builder
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(got.String(), "hi") && time.Now().Before(deadline) {
		n, err := d.ReadTimeout(h, buf, 300*time.Millisecond)
		if err != nil {
			t.Fatalf("ReadTimeout: %v", err)
		}
		got.Write(buf[:n])
	}
	if !strings.Contains(got.String(), "hi") {
		t.Fatalf("output %q does not contain hi", got.String())
	}

	// Drain everything until the process actually goes quiet (the shell
	// prompt is usually still in flight right after the echo).
	for {
		n, err := d.ReadTimeout(h, buf, 200*time.Millisecond)
		if err != nil {
			t.Fatalf("drain ReadTimeout: %v", err)
		}
		if n == 0 {
			break
		}
		got.Write(buf[:n])
	}

	// Output is drained now; bash sits at its prompt, so the read must
	// return (0, nil) after the timeout instead of blocking forever.
	start := time.Now()
	n, err := d.ReadTimeout(h, buf, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("quiet ReadTimeout: %v", err)
	}
	if n != 0 {
		t.Fatalf("quiet read returned %d bytes: %q", n, buf[:n])
	}
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Fatalf("quiet read returned too early: %v", elapsed)
	}
}

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
