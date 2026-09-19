package cli

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/destafajri/smart-routing/internal/config"
	"github.com/destafajri/smart-routing/internal/model"
	"github.com/destafajri/smart-routing/internal/provider"
	"github.com/destafajri/smart-routing/internal/router"
	"github.com/destafajri/smart-routing/internal/state"
)

const Version = "0.1.0"

type App struct {
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer
	Getwd  func() (string, error)
}

func New() *App {
	return &App{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr, Getwd: os.Getwd}
}

func (a *App) Run(ctx context.Context, args []string) int {
	configPath, args := parseConfigFlag(args)
	if configPath == "" {
		configPath = config.DefaultPath()
	}

	if len(args) == 0 {
		return a.interactive(ctx, configPath)
	}

	switch args[0] {
	case "help", "-h", "--help":
		a.printHelp()
		return 0
	case "version", "--version":
		fmt.Fprintf(a.Out, "airun %s\n", Version)
		return 0
	case "init":
		force := len(args) > 1 && args[1] == "--force"
		if err := config.WriteDefault(configPath, force); err != nil {
			fmt.Fprintln(a.ErrOut, "error:", err)
			return 1
		}
		fmt.Fprintf(a.Out, "created %s\n", configPath)
		return 0
	}

	rt, cleanup, err := a.runtime(configPath)
	if err != nil {
		fmt.Fprintln(a.ErrOut, "error:", err)
		return 1
	}
	defer cleanup()

	switch args[0] {
	case "run":
		if len(args) < 2 {
			fmt.Fprintln(a.ErrOut, "usage: airun run <task>")
			return 2
		}
		return a.runTask(ctx, rt, strings.Join(args[1:], " "))
	case "status", "providers":
		return a.status(ctx, rt)
	case "active-provider", "active":
		return a.activeProvider(rt)
	case "queue":
		return a.listTasks(rt, "queue", model.TaskQueued, model.TaskRunning)
	case "failed":
		return a.listTasks(rt, "failed", model.TaskFailed)
	case "resumable":
		return a.listTasks(rt, "resumable", model.TaskPaused)
	case "resume":
		if len(args) < 2 {
			fmt.Fprintln(a.ErrOut, "usage: airun resume <task-id|--all>")
			return 2
		}
		return a.resume(ctx, rt, args[1])
	case "daemon":
		return a.daemon(ctx, rt)
	default:
		fmt.Fprintf(a.ErrOut, "unknown command %q\n\n", args[0])
		a.printHelp()
		return 2
	}
}

type runtime struct {
	cfg     config.Config
	store   *state.Store
	runner  *provider.ExecRunner
	engine  *router.Engine
	workdir string
}

func (a *App) runtime(configPath string) (*runtime, func(), error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, func() {}, fmt.Errorf("config not found: %s (run: airun init)", configPath)
		}
		return nil, func() {}, err
	}
	wd, err := a.Getwd()
	if err != nil {
		return nil, func() {}, err
	}
	stateDir, err := resolveStateDir(cfg, wd)
	if err != nil {
		return nil, func() {}, err
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, func() {}, err
	}
	logFile, err := os.OpenFile(filepath.Join(stateDir, "airun.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, func() {}, err
	}
	store := state.New(filepath.Join(stateDir, "state.json"))
	recovered, err := store.ReconcileOrphanedTasks()
	if err != nil {
		_ = logFile.Close()
		return nil, func() {}, fmt.Errorf("reconcile task state: %w", err)
	}
	if recovered > 0 {
		fmt.Fprintf(logFile, "%s recovered %d orphaned task(s)\n", time.Now().UTC().Format(time.RFC3339), recovered)
	}
	runner := provider.NewExecRunner()
	engine := router.NewEngine(cfg, store, runner, router.EngineOptions{Workdir: wd, Output: a.Out, Log: logFile})
	return &runtime{cfg: cfg, store: store, runner: runner, engine: engine, workdir: wd}, func() { _ = logFile.Close() }, nil
}

func (a *App) runTask(ctx context.Context, rt *runtime, prompt string) int {
	id, err := newTaskID()
	if err != nil {
		fmt.Fprintln(a.ErrOut, "error:", err)
		return 1
	}
	task := model.NewTask(id, prompt)
	if err := rt.store.UpsertTask(task); err != nil {
		fmt.Fprintln(a.ErrOut, "error:", err)
		return 1
	}
	fmt.Fprintf(a.Out, "task %s queued\n", id)
	got, err := rt.engine.Execute(ctx, id)
	if err != nil {
		fmt.Fprintf(a.ErrOut, "task %s %s: %v\n", id, got.Status, err)
		return 1
	}
	fmt.Fprintf(a.Out, "\ntask %s completed by %s\n", id, got.ActiveProvider)
	return 0
}

func (a *App) status(ctx context.Context, rt *runtime) int {
	fmt.Fprintln(a.Out, "PROVIDER\tPRIORITY\tSTATUS")
	for _, p := range rt.cfg.Providers {
		status := "available"
		if err := rt.runner.Health(ctx, p, rt.workdir); err != nil {
			status = "unavailable: " + oneLine(err.Error())
		}
		fmt.Fprintf(a.Out, "%s\t%d\t%s\n", p.Name, p.Priority, status)
	}
	return 0
}

func (a *App) activeProvider(rt *runtime) int {
	tasks, err := rt.store.ListTasks(model.TaskRunning)
	if err != nil {
		fmt.Fprintln(a.ErrOut, "error:", err)
		return 1
	}
	if len(tasks) > 0 && tasks[0].ActiveProvider != "" {
		fmt.Fprintln(a.Out, tasks[0].ActiveProvider)
		return 0
	}
	if len(rt.cfg.Providers) == 0 {
		fmt.Fprintln(a.Out, "none")
		return 0
	}
	fmt.Fprintf(a.Out, "%s (preferred; idle)\n", rt.cfg.Providers[0].Name)
	return 0
}

func (a *App) listTasks(rt *runtime, label string, statuses ...model.TaskStatus) int {
	tasks, err := rt.store.ListTasks(statuses...)
	if err != nil {
		fmt.Fprintln(a.ErrOut, "error:", err)
		return 1
	}
	if len(tasks) == 0 {
		fmt.Fprintf(a.Out, "no %s tasks\n", label)
		return 0
	}
	fmt.Fprintln(a.Out, "ID\tSTATUS\tPROVIDER\tUPDATED\tPROMPT")
	for _, t := range tasks {
		fmt.Fprintf(a.Out, "%s\t%s\t%s\t%s\t%s\n", t.ID, t.Status, t.ActiveProvider, t.UpdatedAt.Local().Format("2006-01-02 15:04:05"), truncateLine(t.Prompt, 80))
	}
	return 0
}

func (a *App) resume(ctx context.Context, rt *runtime, target string) int {
	if target == "--all" {
		tasks, err := rt.store.ListTasks(model.TaskPaused, model.TaskQueued)
		if err != nil {
			fmt.Fprintln(a.ErrOut, "error:", err)
			return 1
		}
		code := 0
		for _, t := range tasks {
			if _, err := rt.engine.Execute(ctx, t.ID); err != nil {
				fmt.Fprintf(a.ErrOut, "%s: %v\n", t.ID, err)
				code = 1
			}
		}
		return code
	}
	if _, ok, err := rt.store.GetTask(target); err != nil {
		fmt.Fprintln(a.ErrOut, "error:", err)
		return 1
	} else if !ok {
		fmt.Fprintf(a.ErrOut, "task %s not found\n", target)
		return 1
	}
	if _, err := rt.engine.Execute(ctx, target); err != nil {
		fmt.Fprintln(a.ErrOut, "error:", err)
		return 1
	}
	return 0
}

func (a *App) daemon(ctx context.Context, rt *runtime) int {
	interval := time.Duration(rt.cfg.PollIntervalSeconds) * time.Second
	fmt.Fprintf(a.Out, "auto-resume daemon running; poll interval %s\n", interval)
	for {
		if err := a.resumeEligible(ctx, rt); err != nil {
			fmt.Fprintln(a.ErrOut, "daemon:", err)
		}
		t := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			t.Stop()
			fmt.Fprintln(a.Out, "daemon stopped")
			return 0
		case <-t.C:
		}
	}
}

func (a *App) resumeEligible(ctx context.Context, rt *runtime) error {
	recovered, err := rt.store.ReconcileOrphanedTasks()
	if err != nil {
		return err
	}
	if recovered > 0 {
		fmt.Fprintf(a.Out, "recovered %d orphaned task(s)\n", recovered)
	}
	tasks, err := rt.store.ListTasks(model.TaskPaused, model.TaskQueued)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if _, err := rt.engine.Execute(ctx, task.ID); err != nil {
			if strings.Contains(err.Error(), "already executing") {
				continue
			}
			fmt.Fprintf(a.ErrOut, "[%s] still paused: %v\n", task.ID, err)
		}
	}
	return nil
}

func (a *App) interactive(ctx context.Context, configPath string) int {
	rt, cleanup, err := a.runtime(configPath)
	if err != nil {
		fmt.Fprintln(a.ErrOut, "error:", err)
		fmt.Fprintln(a.ErrOut, "run `airun init` first if this is a new project")
		return 1
	}
	defer cleanup()
	fmt.Fprintf(a.Out, "AI Run %s — %d providers configured. Type /help for commands.\n", Version, len(rt.cfg.Providers))
	scanner := bufio.NewScanner(a.In)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for {
		fmt.Fprint(a.Out, "airun> ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") {
			parts := strings.Fields(line)
			switch parts[0] {
			case "/quit", "/exit":
				return 0
			case "/help":
				a.printInteractiveHelp()
			case "/status", "/providers":
				a.status(ctx, rt)
			case "/active":
				a.activeProvider(rt)
			case "/queue":
				a.listTasks(rt, "queue", model.TaskQueued, model.TaskRunning)
			case "/failed":
				a.listTasks(rt, "failed", model.TaskFailed)
			case "/resumable":
				a.listTasks(rt, "resumable", model.TaskPaused)
			case "/resume":
				if len(parts) < 2 {
					fmt.Fprintln(a.ErrOut, "usage: /resume <task-id|--all>")
				} else {
					a.resume(ctx, rt, parts[1])
				}
			default:
				fmt.Fprintf(a.ErrOut, "unknown interactive command %s\n", parts[0])
			}
			continue
		}
		a.runTask(ctx, rt, line)
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(a.ErrOut, "input error:", err)
		return 1
	}
	return 0
}

func (a *App) printHelp() {
	fmt.Fprint(a.Out, `airun - smart AI CLI routing and failover

Usage:
  airun                       Start interactive CLI
  airun init [--force]        Create .airun/config.json
  airun run <task>            Execute a task with automatic failover
  airun status                Show provider health/status
  airun active-provider       Show active/preferred provider
  airun queue                 Show queued/running tasks
  airun failed                Show failed tasks
  airun resumable             Show paused/resumable tasks
  airun resume <id|--all>     Resume paused tasks
  airun daemon                Auto-resume paused tasks when providers recover
  airun version               Show version

Global:
  --config <path>             Use a different config file
`)
}

func (a *App) printInteractiveHelp() {
	fmt.Fprint(a.Out, `/status       provider status
/active       active/preferred provider
/queue        queued/running tasks
/failed       failed tasks
/resumable    paused tasks
/resume ID    resume a task (/resume --all supported)
/help         this help
/quit         exit

Any line without / is submitted as a new routed task.
`)
}

func parseConfigFlag(args []string) (string, []string) {
	var out []string
	var path string
	for i := 0; i < len(args); i++ {
		if args[i] == "--config" && i+1 < len(args) {
			path = args[i+1]
			i++
			continue
		}
		out = append(out, args[i])
	}
	return path, out
}

func resolveStateDir(cfg config.Config, workdir string) (string, error) {
	if stateDir := strings.TrimSpace(cfg.StateDir); stateDir != "" {
		if filepath.IsAbs(stateDir) {
			return filepath.Clean(stateDir), nil
		}
		return filepath.Clean(filepath.Join(workdir, stateDir)), nil
	}

	canonical, err := filepath.Abs(workdir)
	if err != nil {
		return "", fmt.Errorf("resolve project path: %w", err)
	}
	canonical, err = filepath.EvalSymlinks(canonical)
	if err != nil {
		return "", fmt.Errorf("canonicalize project path: %w", err)
	}
	canonical = filepath.Clean(canonical)
	if goruntime.GOOS == "windows" {
		canonical = strings.ToLower(canonical)
	}

	root, err := defaultStateHome()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonical))
	projectKey := hex.EncodeToString(sum[:16])
	return filepath.Join(root, "projects", projectKey), nil
}

func defaultStateHome() (string, error) {
	if override := strings.TrimSpace(os.Getenv("AIRUN_STATE_HOME")); override != "" {
		abs, err := filepath.Abs(override)
		if err != nil {
			return "", fmt.Errorf("resolve AIRUN_STATE_HOME: %w", err)
		}
		return filepath.Clean(abs), nil
	}

	switch goruntime.GOOS {
	case "linux":
		if xdg := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); xdg != "" {
			return filepath.Join(xdg, "airun"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home for state: %w", err)
		}
		return filepath.Join(home, ".local", "state", "airun"), nil
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home for state: %w", err)
		}
		return filepath.Join(home, "Library", "Application Support", "airun", "state"), nil
	default:
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("resolve user state directory: %w", err)
		}
		return filepath.Join(dir, "airun", "state"), nil
	}
}

func newTaskID() (string, error) {
	var b [5]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d-%s", time.Now().UTC().Unix(), hex.EncodeToString(b[:])), nil
}
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
func truncateLine(s string, n int) string {
	s = oneLine(s)
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
