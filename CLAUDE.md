# pingm: notes for Claude

A live terminal table of ICMP reachability, in Go. `README.md` is the user
documentation; this file is what a new session needs on top of it.

## Build and test

```bash
go build -o pingm .
go vet ./...
go test -race ./...            # includes pty tests that run the real binary
go test -short ./...           # skips the pty tests
GOOS=windows go vet ./...      # the netstate and monitor packages have per-OS files
GOOS=linux go vet ./...
```

CI runs on Linux, macOS and Windows (`.github/workflows/test.yml`). Releasing
is in the README.

## Layout

- `main.go` wires everything; every outside dependency is a field on `session` so `run` can be tested with fakes (`main_test.go`, `sessionlog_test.go`).
- `internal/monitor` probes and keeps stats. `internal/ui` is the Bubble Tea table.
- `internal/export` is the `-o` CSV log.
- `internal/journal` is the session log: event types, writer (also a `monitor.Recorder`), the 2-second watch loop, and `-report`.
- `internal/netstate` reads interfaces, gateway, DNS and Wi-Fi per OS.
- `internal/shellhist` tails shell history files and masks secrets.

## Session logs: use them to improve pingm

The session log is opt-in (`PINGM_SESSION_LOG=1`) so other users get no new
files. The owner has it on in `~/.zshrc`, with `PINGM_HISTORY` pointing at
Orca's per-terminal history files and zsh set to save each command as it runs.

The point of the log is to learn from connections pingm could not explain or
fix. When asked to look at them:

1. Logs are in `~/Library/Logs/pingm/*.jsonl` on the owner's Mac. `pingm -report` summarises the newest; `pingm -report FILE` an older one.
2. For each incident, look at the `net`, `cmd` and `note` events between the drop and the recovery. They show what the owner did and what changed on the machine.
3. Turn a repeated fix into a feature, for example detecting that state or suggesting that step, and say which logs it came from.

The logs contain shell commands. Read them locally. Do not paste them into
issues, PRs or anything outside this machine without asking.
