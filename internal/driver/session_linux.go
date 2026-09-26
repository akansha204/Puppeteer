//go:build linux

package driver

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// signalSessionProcessGroups signals every process group whose session matches
// sessionID. A PTY shell can place jobs into their own groups while keeping
// them in the terminal session, so stopping only the session leader's group
// would leave those jobs behind. Descendants that call setsid() leave the
// session entirely and are not covered: process groups by themselves are not a
// security boundary.
func signalSessionProcessGroups(sessionID int, sig syscall.Signal) error {
	pgids := map[int]struct{}{
		sessionID: {},
	}

	entries, scanErr := os.ReadDir("/proc")
	if scanErr == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			pid, err := strconv.Atoi(entry.Name())
			if err != nil {
				continue
			}

			raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
			if err != nil {
				continue
			}

			stat := string(raw)
			closeParen := strings.LastIndex(stat, ")")
			if closeParen < 0 {
				continue
			}

			// After "(comm)": state, ppid, pgrp, session, ...
			fields := strings.Fields(stat[closeParen+1:])
			if len(fields) < 4 {
				continue
			}

			pgrp, err1 := strconv.Atoi(fields[2])
			sid, err2 := strconv.Atoi(fields[3])
			if err1 != nil || err2 != nil {
				continue
			}

			if sid == sessionID && pgrp > 0 {
				pgids[pgrp] = struct{}{}
			}
		}
	}

	var firstErr error

	for pgid := range pgids {
		if err := syscall.Kill(-pgid, sig); err != nil &&
			!errors.Is(err, syscall.ESRCH) &&
			firstErr == nil {
			firstErr = err
		}
	}

	if firstErr != nil {
		return firstErr
	}

	if scanErr != nil {
		return fmt.Errorf("scan /proc for session %d: %w", sessionID, scanErr)
	}

	return nil
}
