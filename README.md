# Smart Routing (`airun`)

`airun` is a local, deterministic AI CLI router. It runs a task through a configurable provider priority list and automatically fails over for explicitly classified infrastructure failures such as quota/rate limits, timeouts, outages, or unavailable providers. Unmatched failures fail closed by default.

It is designed for coding-agent CLIs such as Claude Code, Codex CLI, Gemini CLI, or any other command-line agent that can accept a prompt non-interactively.

> **Status:** MVP (`v0.1.0`). The routing/state engine and CLI are tested locally. Real provider end-to-end behavior still depends on the provider CLI version, authentication, output format, and error text installed on your machine.

## Features

- Any number of providers/models; no hardcoded provider limit.
- Deterministic priority/fallback order.
- Generic provider adapter: executable + arguments + prompt mode.
- Detects quota, rate limit, timeout, outage, unavailable command, auth, configured provider failures, and unknown failures; unknown failures fail closed by default.
- Per-provider custom error patterns and failover policies.
- Retries with exponential backoff before failover where appropriate.
- Preserves task context across providers using the original task, previous attempts, and current Git working-tree summary.
- Saves task/execution state locally in `.airun/state.json`.
- Prevents the same task from running concurrently with task lock files.
- Pauses unfinished tasks when all providers are exhausted.
- `airun daemon` automatically retries paused tasks when providers become usable again.
- Interactive REPL and one-shot CLI modes.
- Local event log at `.airun/airun.log`.

## How failover works

```text
Task
  |
  v
Provider priority #1
  | quota / 429 / timeout / outage / unavailable
  v
retry (when useful) -> exponential backoff
  |
  v
Provider priority #2
  |
  v
Provider priority #3 ... N
  |
  +--> success -> completed
  |
  +--> all exhausted -> paused/resumable
```

On a provider handoff, `airun` asks the next provider to inspect existing work first and includes:

- the original task;
- recent provider attempts/failure reasons;
- `git status --short --branch`;
- `git diff --stat`.

The repository itself remains the source of truth. `airun` does **not** try to transplant proprietary Claude/Codex/Gemini session IDs between different products.

## Requirements

- Go 1.23+ to build/install `airun` from source.
- At least one supported AI CLI installed and authenticated on your machine.
- Git is recommended for safe handoff context, but `airun` can still run outside a Git repository.

Provider CLIs are separate products. Install and authenticate them according to their official documentation before using them through `airun`.

## Install

### Option 1: Go install

After Go is installed:

```bash
go install github.com/destafajri/smart-routing/cmd/airun@latest
```

Make sure the Go binary directory is on your `PATH`.

macOS/Linux commonly uses:

```bash
export PATH="$PATH:$(go env GOPATH)/bin"
```

On Windows, add this directory to your user `PATH`:

```text
%USERPROFILE%\go\bin
```

Verify:

```bash
airun version
```

### Option 2: Clone and build

```bash
git clone https://github.com/destafajri/smart-routing.git
cd smart-routing
go test ./...
go build -o airun ./cmd/airun
```

macOS/Linux:

```bash
sudo mv airun /usr/local/bin/airun
```

Windows PowerShell:

```powershell
go build -o airun.exe ./cmd/airun
# Move airun.exe to a directory that is on PATH.
```

## Quick start

Go to the project/repository where you want AI agents to work:

```bash
cd /path/to/your/project
airun init
```

This creates:

```text
.airun/config.json
```

Default priority:

```text
Claude Code -> Codex CLI -> Gemini CLI
```

Check providers:

```bash
airun status
```

Run one task:

```bash
airun run "implement refresh-token rotation and run the existing tests"
```

Or enter interactive mode:

```bash
airun
```

Then type normal task text:

```text
airun> fix the failing authentication tests
```

Slash commands in interactive mode:

```text
/status
/active
/queue
/failed
/resumable
/resume <task-id>
/resume --all
/help
/quit
```

## Configuration

Default `.airun/config.json`:

```json
{
  "state_dir": ".airun",
  "poll_interval_seconds": 60,
  "providers": [
    {
      "name": "claude",
      "priority": 10,
      "command": "claude",
      "args": ["-p", "{{prompt}}"],
      "prompt_mode": "arg",
      "timeout_seconds": 1800,
      "max_retries": 1,
      "retry_backoff_millis": 1500,
      "health_args": ["--version"]
    },
    {
      "name": "codex",
      "priority": 20,
      "command": "codex",
      "args": ["exec", "{{prompt}}"],
      "prompt_mode": "arg",
      "timeout_seconds": 1800,
      "max_retries": 1,
      "retry_backoff_millis": 1500,
      "health_args": ["--version"]
    },
    {
      "name": "gemini",
      "priority": 30,
      "command": "gemini",
      "args": ["-p", "{{prompt}}"],
      "prompt_mode": "arg",
      "timeout_seconds": 1800,
      "max_retries": 1,
      "retry_backoff_millis": 1500,
      "health_args": ["--version"]
    }
  ]
}
```

The CLI syntax above matches the common non-interactive entry points (`claude -p`, `codex exec`, `gemini -p`) at the time this project was created. If your installed CLI version differs, edit `command`/`args`; the router itself is provider-agnostic.

### Add unlimited providers

Just add more objects. Priority is ascending: lower number runs first.

```json
{
  "name": "my-agent",
  "priority": 40,
  "command": "my-agent",
  "args": ["run", "{{prompt}}"],
  "timeout_seconds": 1200,
  "max_retries": 2
}
```

No router code change is required.

### Send the prompt through stdin

Useful for CLIs that support piped prompts or when prompts can become very large:

```json
{
  "name": "custom",
  "priority": 50,
  "command": "custom-ai",
  "args": ["--non-interactive"],
  "prompt_mode": "stdin"
}
```

### Custom quota/error patterns

Provider messages change over time. Add provider-specific matching without changing Go code:

```json
{
  "name": "example",
  "priority": 60,
  "command": "example-ai",
  "args": ["{{prompt}}"],
  "error_patterns": {
    "quota": ["credits exhausted", "plan limit reached"],
    "rate_limit": ["slow down"],
    "outage": ["temporarily unavailable"]
  }
}
```

Built-in failure kinds:

```text
quota
rate_limit
timeout
outage
unavailable
auth
provider
```

### Control which failures trigger failover

By default, automatic failover is limited to `quota`, `rate_limit`, `timeout`, `outage`, and `unavailable`. Auth failures, explicitly configured generic provider failures, and unmatched/unknown failures do not fail over unless you deliberately opt in where supported. Override per provider:

```json
{
  "name": "example",
  "priority": 10,
  "command": "example-ai",
  "args": ["{{prompt}}"],
  "failover_on": ["quota", "rate_limit", "timeout", "outage", "unavailable"]
}
```

An error not included in `failover_on` marks the task `failed` instead of silently moving to another provider.

## CLI commands

```bash
airun status
airun active-provider
airun queue
airun failed
airun resumable
airun resume <task-id>
airun resume --all
```

Use a different config file:

```bash
airun --config /path/to/config.json status
```

`AIRUN_CONFIG` can also set the config path:

```bash
export AIRUN_CONFIG="$HOME/.config/airun/config.json"
```

## Automatic resume

If all providers are unavailable/exhausted, the task becomes `paused` rather than being discarded.

Run:

```bash
airun daemon
```

The daemon reconciles orphaned `running` tasks after crashes/restarts, then checks queued/paused tasks every `poll_interval_seconds` and retries them. Duplicate execution is prevented by a per-task filesystem lock with stale-owner detection, so another live `airun` process cannot execute the same task at the same time.

For a machine that should resume tasks continuously, run `airun daemon` under your normal process supervisor (systemd, launchd, Windows Task Scheduler, Docker, etc.). The project intentionally does not install a background service automatically.

## Local files

By default:

```text
.airun/
├── config.json     # provider configuration
├── state.json      # task/execution state
├── airun.log       # routing/retry/failover events
└── locks/          # duplicate-execution locks
```

`state.json` and logs may contain task/error text. Treat `.airun/` as local/private state and normally add it to your project's `.gitignore`.

Do not store API keys in `config.json`. Provider subprocesses inherit your existing shell environment, so use the provider's normal login flow or environment variables/secret manager.

## Safety and limitations

`airun` prevents **concurrent duplicate execution of the same task**, but it cannot make arbitrary external side effects transactional. A provider could, for example, successfully deploy or run a migration and then crash before `airun` observes a clean exit. The next provider cannot always prove whether that external action already happened.

For operations such as production deploys, database migrations, payments, publishing, destructive cloud commands, or external API mutations:

- make operations idempotent where possible;
- use provider/tool approval controls;
- use idempotency keys for external APIs when supported;
- inspect state before replaying a dangerous operation;
- do not assume failover is equivalent to a distributed transaction coordinator.

`airun` also does not promise that error wording from every future provider release will match built-in patterns. Use `error_patterns` when a provider changes its messages. State writes use a temporary file plus rename; exact atomic replacement/durability guarantees remain platform/filesystem dependent.

On timeout/cancellation, provider processes are started in an OS-specific process group/tree and `airun` attempts to terminate the full tree before retry/failover. If tree termination itself cannot be guaranteed, routing fails closed instead of starting another provider.

## Development

```bash
go test ./...
go vet ./...
go build ./cmd/airun
```

The routing engine is intentionally separate from the process adapter so it can be tested with fake providers without consuming AI quota.

Project layout:

```text
cmd/airun/          executable
internal/cli/       CLI + interactive REPL
internal/config/    config parsing/validation
internal/provider/  generic process adapter
internal/router/    classification, retry, failover, handoff
internal/state/     persistent task state + locks
internal/gitctx/    Git handoff context
```

## License

MIT. See [LICENSE](LICENSE).
