package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/akansha204/pony/internal/agent"
)

func main() {
	mgr := agent.NewManager()

	defer func() {
		for _, a := range mgr.GetAgents() {
			if err := mgr.Stop(a); err != nil {
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
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}

		switch fields[0] {
		case "start":
			if len(fields) < 3 {
				fmt.Println("usage: start <id> <command> [args...]")
				continue
			}
			a := &agent.Agent{ID: fields[1], Command: fields[2], Args: fields[3:]}
			if err := mgr.Start(a); err != nil {
				fmt.Println("start:", err)
				continue
			}
			fmt.Printf("started pid=%d status=%s\n", a.PID, mgr.StatusOf(a))

		case "stop":
			a, ok := mgr.Get(fields[1])
			if !ok {
				fmt.Printf("no agent %q\n", fields[1])
				continue
			}
			if err := mgr.Stop(a); err != nil {
				fmt.Println("stop:", err)
				continue
			}
			fmt.Printf("stopped %s\n", a.ID)

		case "restart":
			a, ok := mgr.Get(fields[1])
			if !ok {
				fmt.Printf("no agent %q\n", fields[1])
				continue
			}
			if err := mgr.Restart(a); err != nil {
				fmt.Println("restart:", err)
				continue
			}
			fmt.Printf("restarted pid=%d status=%s\n", a.PID, mgr.StatusOf(a))

		case "status":
			for id, a := range mgr.GetAgents() {
				fmt.Printf("%-10s pid=%-7d status=%s\n", id, a.PID, mgr.StatusOf(a))
			}

		case "quit", "exit":
			return

		default:
			fmt.Println("commands: start <id> <cmd> [args...] | stop <id> | restart <id> | status | quit")
		}
	}
}
