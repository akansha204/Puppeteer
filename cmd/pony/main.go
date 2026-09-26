package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/akansha204/pony/internal/agent"
	"github.com/akansha204/pony/internal/driver"
	"github.com/akansha204/pony/internal/shell_lexer"
)

func main() {
	mgr := agent.NewManager(driver.NewPTYDriver())

	defer func() {
		for _, snap := range mgr.Snapshots() {
			if err := mgr.Stop(snap.AgentID); err != nil {
				fmt.Println("cleanup:", err)
			}
		}
	}()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("pony> ")
		if !scanner.Scan() {
			break
		}
		fields, err := shell_lexer.Fields(scanner.Text())
		if err != nil {
			fmt.Println("malformed input:", err)
			continue
		}
		if len(fields) == 0 {
			continue
		}

		switch fields[0] {
		case "start":
			if len(fields) < 3 {
				fmt.Println("usage: start <id> <command> [args...]")
				continue
			}
			spec := agent.AgentSpec{ID: agent.AgentID(fields[1]), Command: fields[2], Args: fields[3:]}
			snap, err := mgr.Start(spec)
			if err != nil {
				fmt.Println("start:", err)
				continue
			}
			fmt.Printf("started pid=%d state=%s\n", snap.PID, snap.State)

		case "stop":
			if len(fields) != 2 {
				fmt.Println("usage: stop <id>")
				continue
			}
			id := agent.AgentID(fields[1])
			if err := mgr.Stop(id); err != nil {
				fmt.Println("stop:", err)
				continue
			}
			fmt.Printf("stopped %s\n", id)

		case "restart":
			if len(fields) != 2 {
				fmt.Println("usage: restart <id>")
				continue
			}
			id := agent.AgentID(fields[1])
			if err := mgr.Restart(id); err != nil {
				fmt.Println("restart:", err)
				continue
			}
			snap, _ := mgr.Get(id)
			fmt.Printf("restarted pid=%d state=%s\n", snap.PID, snap.State)

		case "send":
			if len(fields) < 3 {
				fmt.Println("usage: send <id> <text>")
				continue
			}
			id := agent.AgentID(fields[1])
			text := strings.Join(fields[2:], " ")
			if _, err := mgr.Write(id, []byte(text+"\n")); err != nil {
				fmt.Println("send:", err)
				continue
			}
			fmt.Printf("sent %s: %s\n", id, text)

		case "read":
			if len(fields) != 2 {
				fmt.Println("usage: read <id>")
				continue
			}
			readAgent(mgr, agent.AgentID(fields[1]))

		case "resize":
			if len(fields) != 4 {
				fmt.Println("usage: resize <id> <rows> <cols>")
				continue
			}

			rows, err1 := strconv.Atoi(fields[2])
			cols, err2 := strconv.Atoi(fields[3])

			if err1 != nil || err2 != nil {
				fmt.Println("resize: rows and cols must be numbers")
				continue
			}

			if rows < 1 || rows > 65535 || cols < 1 || cols > 65535 {
				fmt.Println("resize: rows and cols must be in the range 1..65535")
				continue
			}

			id := agent.AgentID(fields[1])

			if err := mgr.Resize(id, uint16(rows), uint16(cols)); err != nil {
				fmt.Println("resize:", err)
				continue
			}

			fmt.Printf("resized %s to %dx%d\n", id, rows, cols)

		case "status":
			for _, snap := range mgr.Snapshots() {
				fmt.Printf("%-10s pid=%-7d state=%s\n", snap.AgentID, snap.PID, snap.State)
			}

		case "quit", "exit":
			return

		default:
			fmt.Println("commands: start <id> <cmd> [args...] | send <id> <text> | read <id> | resize <id> <rows> <cols> | stop <id> | restart <id> | status | quit")
		}
	}

}

// readAgent drains an agent's terminal: it blocks up to 200ms per chunk,
// prints every byte as it arrives, and stops once the agent has been quiet
// for a full chunk or a hard 2s cap is hit. An idle agent yields nothing. A
// read error is only reported when nothing was captured: dying mid-stream
// (the pty master returns EIO as the process exits) is normal and the bytes
// we did get are still printed.
func readAgent(mgr *agent.Manager, id agent.AgentID) {
	const chunkWait = 200 * time.Millisecond

	buf := make([]byte, 4096)
	var out strings.Builder
	deadline := time.Now().Add(2 * time.Second)
	var readErr error
	for time.Now().Before(deadline) {
		n, err := mgr.ReadTimeout(id, buf, chunkWait)
		if err != nil {
			readErr = err
			break
		}
		if n == 0 {
			break // quiet: everything pending has come through
		}
		out.Write(buf[:n])
	}

	fmt.Print(out.String())
	if out.Len() > 0 {
		fmt.Println()
	} else if readErr != nil {
		fmt.Println("read:", readErr)
	}
}
