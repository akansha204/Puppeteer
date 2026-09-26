package main

import (
	"bufio"
	"fmt"
	"os"

	"github.com/akansha204/pony/internal/agent"
	"github.com/akansha204/pony/internal/driver"
	"github.com/akansha204/pony/internal/shell_lexer"
)

func main() {
	mgr := agent.NewManager(driver.NewProcessDriver())

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

		case "status":
			for _, snap := range mgr.Snapshots() {
				fmt.Printf("%-10s pid=%-7d state=%s\n", snap.AgentID, snap.PID, snap.State)
			}

		case "quit", "exit":
			return

		default:
			fmt.Println("commands: start <id> <cmd> [args...] | stop <id> | restart <id> | status | quit")
		}
	}
}
