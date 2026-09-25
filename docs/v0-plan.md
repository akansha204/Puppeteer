# Pony v0 — Process Supervisor

Published spec for the `v0.1.0` release.

Pony v0 is a working **process supervisor**: a small Go runtime that
can start, observe, stop, and restart OS processes that stand in for
coding agents.

## v0 goal

Pony should be able to:

1.  Start an agent
2.  Keep its process/session alive
3.  Observe its state
4.  Stop/restart it
5.  Manage multiple agents independently

## Delivered

-   [x] Start a real OS process
-   [x] Track PID and runtime state
-   [x] Stop and restart
-   [x] Monitor natural process exit
-   [x] Detect crashes
-   [x] Manage multiple agents in one manager
-   [x] Avoid double `Wait()`
-   [x] Protect manager state with a mutex
-   [x] Race-tested lifecycle behavior
-   [x] Small REPL CLI
-   [x] CI with `gofmt`, `go mod tidy`, `go vet`, and `go test -race`

## Architecture

``` text
CLI
 ↓
Manager
 ↓
OS process
 ↘
  monitor goroutine
```

Role of each layer:

-   **CLI** — REPL (`pony>`) translating commands into manager calls;
    owns process-wide teardown on exit.
-   **Manager** — the only thing allowed to mutate agent state (single
    `sync.Mutex`). Commands in (`Start`/`Stop`/`Restart`), queries out
    (`Get`/`GetAgents`/`StatusOf`).
-   **Monitor** — one goroutine per agent parked in `cmd.Wait()`;
    converts "process died" into a truthful status and reaps the child.

## Lifecycle state machine

``` text
idle ──Start──► running ──Stop──► stopped ──Start──► running
                   │
                   │ process exits on its own
                   ▼
                crashed ──Start──► running
```

`Restart` = `Stop` then `Start`. It works from any non-running state
and always produces a fresh generation of the process.

## Repository layout

``` text
cmd/pony/main.go            REPL CLI: start | stop | restart | status | quit
internal/agent/agent.go     Agent type + Status enum
internal/agent/manager.go   Manager lifecycle + monitor goroutine
internal/agent/manager_test.go   4 lifecycle tests (test -race clean)
.github/workflows/ci.yml    gofmt · go mod tidy · go vet · go test -race
Makefile                    build / run / test / fmt / check / clean
go.mod                      module github.com/akansha204/pony
```

## Tests

-   [x] `TestStartSpawnsRealProcess` — real PID, `running`, alive at OS level
-   [x] `TestStopTerminatesProcess` — dead after stop, PID zeroed
-   [x] `TestRestartSpawnsFreshProcess` — old PID dead, new PID alive
-   [x] `TestMonitorDetectsCrash` — unexpected exit flags `crashed`
-   [x] `go test -race ./...` green locally and in CI

## Known limits (v0 boundary)

These are deliberately deferred, not regressions:

-   Multi-agent independence is modeled but not yet proven by tests
    (that is **Phase 1** of the v1 roadmap).
-   No `SIGTERM → grace → SIGKILL` escalation: `Stop` blocks if a
    process ignores `SIGTERM` (Phase 2).
-   PID is treated as agent identity; no sessions or generations yet
    (Phase 3).
-   No output capture or PTY — stdin/stdout are discarded.
-   No worktrees, tasks, events, or validation (Phases 7–10).

## Try it

``` sh
make run
```

``` text
pony> start sleepy sleep 1000
pony> status
pony> stop sleepy
pony> quit
```

Cross-check with `ps -C sleep` from another shell.