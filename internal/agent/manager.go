package agent

import "fmt"

type Manager struct {
	agents map[string]*Agent
}

func (m *Manager) Start(a *Agent) error {
	if a.Status == StatusIdle {
		a.Status = StatusStarting
		fmt.Print("agent starting")
	}

	return nil
}

func (m *Manager) Stop(a *Agent) error {
	if a.Status == StatusRunning {
		a.Status = StatusStopped
		fmt.Print("agent stopped")
	}

	return nil
}

func (m *Manager) Restart(a *Agent) error {
	if a.Status == StatusStopped || a.Status == StatusCrashed {
		a.Status = StatusStarting
		fmt.Print("agent restarting")
	}
	return nil
}

func (m *Manager) GetAgents() map[string]*Agent {
	return m.agents
}
