# Smart Routing (`airun`)

`airun` is a local, deterministic AI CLI router. It runs a task through a configurable provider priority list and automatically fails over for explicitly classified infrastructure failures such as quota/rate limits, timeouts, outages, or unavailable providers. Unmatched failures fail closed by default.

It is designed for coding-agent CLIs such as Claude Code, Codex CLI, Gemini CLI, or any other command-line agent that can accept a prompt non-interactively.

> **Status:** MVP (`v0.1.0`). The routing/state engine and CLI are tested locally. Real provider end-to-end behavior still depends on the provider CLI version, authentication, output format, and error text installed on your machine.

## Features

- Any number of providers/models; no hardcoded provider limit.
- Deterministic priority/fallback order.
- Generic provider adapter: executable + arguments + prompt mode.
- Detects quota, rate limit, timeout, outage, unavailable command, auth, configured provider failures, and unknown failures. Automatic classification uses the provider diagnostic/error channel rather than arbitrary task stdout; unknown failures fail closed by default.
- Per-provider custom error patterns and failover policies.
- Retries with exponential backoff before failover where appropriate.
- Preserves task context across providers using the original task, previous attempts, and current Git working-tree summary.
- Saves runtime task/execution state in a per-project user-state directory outside the provider working tree by default.
- Prevents the same task from running concurrently with task lock files.
- Pauses unfinished tasks when all providers are exhausted.
- `airun daemon` automatically retries paused tasks when providers become usable again.
- Interactive REPL and one-shot CLI modes.
- Interactive provider configuration with `airun setup` / `airun configure`: add, edit, remove, reprioritize, and save providers without editing JSON manually.
- Startup warnings when a configured provider executable is not installed or not available on `PATH`.
- Stores the runtime event log with the external per-project control state.

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

### Update / upgrade an existing installation

If you originally installed `airun` with `go install`, upgrade to the latest published version with:

```bash
go install github.com/destafajri/smart-routing/cmd/airun@latest
```

Then verify the binary that your shell resolves:

```bash
airun version
```

If the version did not change, check which binary is being executed:

macOS/Linux:

```bash
which airun
```

Windows PowerShell:

```powershell
Get-Command airun
```

Make sure that path points to the Go-installed binary (commonly `$(go env GOPATH)/bin/airun` on macOS/Linux or `%USERPROFILE%\\go\\bin\\airun.exe` on Windows), or replace the older copy that appears earlier on your `PATH`.

If you installed from a cloned repository instead, update the source and rebuild:

```bash
cd /path/to/smart-routing
git pull
go test ./...
go build -o airun ./cmd/airun
```

Then replace the previously installed binary with the newly built one using the same location you chose during installation.

Your project configuration and external runtime state are not removed by replacing the `airun` executable.

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

To configure providers interactively instead of editing JSON:

```bash
airun setup
```

The setup menu lets you add, edit, remove, and reprioritize any number of providers. `airun configure` is an alias.

When a configured executable is missing, normal `airun` commands print a warning such as:

```text
warning: provider "gemini" command "gemini" is not installed or not available in PATH; install it or run `airun setup` to update provider settings.
```

The warning is informational: routing/health checks still determine which configured providers can actually be used.

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

### Interactive provider setup

Run:

```bash
airun setup
```

The wizard works both for a new project and an existing config. Its menu supports:

```text
1) Add provider
2) Edit provider
3) Remove provider
4) Save and exit
5) Exit without saving
```

For each provider it prompts for the provider name, CLI command, arguments, prompt mode, priority, health-check arguments, timeout, retry count, and retry backoff. Known provider names (`claude`, `codex`, and `gemini`) receive sensible command/argument defaults; custom provider names remain fully configurable.

Press Enter to keep the shown value. Enter `-` for **Arguments** or **Health-check arguments** to clear that list completely—for example, a `prompt_mode=stdin` provider can have zero CLI arguments and receive the prompt only on stdin. Arguments entered in the wizard are otherwise whitespace-separated. For unusual arguments that themselves contain spaces, edit the JSON array directly after setup.

Default `.airun/config.json`:

```json
{
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

### Runtime state location

By default, `state_dir` is omitted. `airun` keeps runtime control state **outside the provider working tree**, under an OS user-state location keyed by the canonical project path. This keeps `state.json`, logs, and advisory lock files away from normal agent edits and repository cleanup commands.

You can override the root used for automatic per-project state:

```bash
export AIRUN_STATE_HOME="/path/to/airun-state"
```

Or set `state_dir` explicitly in project config:

```json
{
  "state_dir": "/absolute/path/to/airun-state"
}
```

A relative explicit `state_dir` is resolved from the project working directory for compatibility. Putting runtime state inside the provider working tree weakens duplicate-execution/recovery isolation and is not recommended.

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
airun setup
airun configure
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

The daemon reconciles orphaned `running` tasks after crashes/restarts, then checks queued/paused tasks every `poll_interval_seconds` and retries them. Duplicate execution is prevented by OS-managed advisory file locks held on open handles. The OS releases those locks when a process exits, so recovery does not delete lock paths based on PID/staleness guesses.

For a machine that should resume tasks continuously, run `airun daemon` under your normal process supervisor (systemd, launchd, Windows Task Scheduler, Docker, etc.). The project intentionally does not install a background service automatically.

## Local files

Project-local configuration remains:

```text
<project>/.airun/config.json
```

Runtime control files are outside the provider working tree by default and are namespaced by a hash of the canonical project path:

```text
<user-state>/airun/.../projects/<project-key>/
├── state.json      # task/execution state
├── airun.log       # routing/retry/failover events
└── locks/          # OS advisory-lock files
```

Typical automatic roots are `$XDG_STATE_HOME/airun` (or `~/.local/state/airun`) on Linux, `~/Library/Application Support/airun/state` on macOS, and the user's application-config area under `airun/state` on Windows. On Linux, a relative `XDG_STATE_HOME` is ignored so automatic control state cannot accidentally become relative to the provider working tree. `AIRUN_STATE_HOME` explicitly overrides this root.

`state.json` and logs may contain task/error text and should be treated as private local data. Do not store API keys in `config.json`. Provider subprocesses inherit your existing shell environment, so use the provider's normal login flow or environment variables/secret manager.

## Safety and limitations

`airun` prevents **concurrent duplicate execution of the same task**, but it cannot make arbitrary external side effects transactional. A provider could, for example, successfully deploy or run a migration and then crash before `airun` observes a clean exit. The next provider cannot always prove whether that external action already happened.

For operations such as production deploys, database migrations, payments, publishing, destructive cloud commands, or external API mutations:

- make operations idempotent where possible;
- use provider/tool approval controls;
- use idempotency keys for external APIs when supported;
- inspect state before replaying a dangerous operation;
- do not assume failover is equivalent to a distributed transaction coordinator.

`airun` also does not promise that error wording from every future provider release will match built-in patterns. Use `error_patterns` when a provider changes its diagnostic messages. Provider stdout is treated as task output and is not used by default to infer infrastructure failures. State writes use a temporary file plus rename; exact atomic replacement/durability guarantees remain platform/filesystem dependent.

On timeout/cancellation, provider processes are started in an OS-specific process group/tree and `airun` attempts to terminate the full tree before retry/failover. If tree termination itself cannot be guaranteed, routing fails closed instead of starting another provider.

Keeping control state outside the working tree protects it from ordinary provider edits and cleanup commands, but it is **not a security boundary against a malicious process running as the same OS user**. A same-user process may still be able to access user-state files directly. Use an OS/container sandbox or a separate restricted user account if hostile-provider isolation is required.

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
