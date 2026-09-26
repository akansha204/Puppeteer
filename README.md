# Pony

Pony is a lightweight Go runtime for running and managing
multiple AI coding agents.

## v0 goal

Pony should be able to:

1. Start an agent
2. Keep its process/session alive
3. Observe its state
4. Stop/restart it
5. Manage multiple agents independently

## Platform

V1 targets **Linux only**. Process-group termination (`Setpgid`,
`SIGTERM`/`SIGKILL`), exit-signal decoding, and the `/proc/<pid>`
checks used by tests are Linux/Unix-specific behaviors. Cross-platform
support is a later milestone (platform-tagged files such as
`process_unix.go` / `process_windows.go`).

## Development

Requires Go 1.26+.

```sh
make build      # compile to bin/pony
make run        # run the CLI
make test       # run tests with the race detector
make check      # fmt + tidy + vet + test (mirrors CI)
```