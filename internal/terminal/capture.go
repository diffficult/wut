// Package terminal captures the active tmux or GNU screen pane and extracts
// shell-aware command context from it.
package terminal

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Supported terminal multiplexers.
const (
	MultiplexerTmux   = "tmux"
	MultiplexerScreen = "screen"
)

// Shells whose prompt can be resolved.
var knownShells = []string{
	"bash",
	"fish",
	"zsh",
	"csh",
	"tcsh",
	"powershell",
	"pwsh",
}

// Sentinel errors for capture construction.
var (
	ErrNoMultiplexer          = errors.New("wut must be run inside a tmux or screen session")
	ErrUnsupportedMultiplexer = errors.New("unsupported terminal multiplexer")
	ErrNoRunner               = errors.New("terminal: nil command runner")
	ErrNoPaneOutput           = errors.New("no output captured from the pane")
	ErrNoProcessTable         = errors.New("no process table available")
)

// Runner executes external commands. Output captures stdout as a string;
// RunToFile redirects stdout into outPath.
type Runner interface {
	Output(name string, args []string) (string, error)
	RunToFile(name string, args []string, outPath string) error
}

// Shell describes the interactive shell hosting the current session.
type Shell struct {
	Path   string
	Name   string
	Prompt string
}

// ProcessTree exposes process names and parents, so shell discovery can be
// exercised without touching the host.
type ProcessTree interface {
	name(pid int) string
	parent(pid int) (int, bool)
}

// PsProcessTable is a snapshot of process metadata. It is read through the POSIX
// ps utility, which is available on Linux, macOS and the BSDs, so shell
// discovery does not depend on /proc.
type PsProcessTable struct {
	entries map[int]psEntry
}

type psEntry struct {
	ppid int
	comm string
}

// PsCommand is the ps invocation used to snapshot the process table. The flags
// are portable: "ax" lists every process and empty -o headers suppress the
// header line on both BSD and procps ps.
var PsCommand = []string{"ps", "ax", "-o", "pid=,ppid=,comm="}

// NewPsProcessTable snapshots the process table through runner. A missing or
// unusable ps is reported as ErrNoProcessTable so callers can degrade instead of
// failing.
func NewPsProcessTable(runner Runner) (PsProcessTable, error) {
	if runner == nil {
		return PsProcessTable{}, ErrNoRunner
	}

	out, err := runner.Output(PsCommand[0], PsCommand[1:])
	if err != nil {
		return PsProcessTable{}, fmt.Errorf("%w: %v", ErrNoProcessTable, err)
	}

	table, err := parsePsOutput(out)
	if err != nil {
		return PsProcessTable{}, err
	}
	return table, nil
}

// parsePsOutput reads "pid ppid command" lines, skipping anything malformed.
func parsePsOutput(out string) (PsProcessTable, error) {
	table := PsProcessTable{entries: map[int]psEntry{}}

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}

		// The command may contain spaces, so it owns the rest of the line.
		comm := strings.Join(fields[2:], " ")
		table.entries[pid] = psEntry{ppid: ppid, comm: comm}
	}

	if len(table.entries) == 0 {
		return PsProcessTable{}, fmt.Errorf("%w: ps returned no usable process rows", ErrNoProcessTable)
	}
	return table, nil
}

// Name returns the command of pid, or "" when pid is unknown.
func (t PsProcessTable) Name(pid int) string {
	entry, ok := t.entries[pid]
	if !ok {
		return ""
	}
	return entry.comm
}

// Parent returns the parent pid of pid. A process without a parent in the table
// or with pid 0 as its parent has no reported parent.
func (t PsProcessTable) Parent(pid int) (int, bool) {
	entry, ok := t.entries[pid]
	if !ok || entry.ppid <= 0 {
		return 0, false
	}
	return entry.ppid, true
}

// Tree adapts the snapshot to the ProcessTree interface.
func (t PsProcessTable) Tree() ProcessTree { return psProcessTree{table: t} }

type psProcessTree struct {
	table PsProcessTable
}

func (p psProcessTree) name(pid int) string { return p.table.Name(pid) }

func (p psProcessTree) parent(pid int) (int, bool) { return p.table.Parent(pid) }

// DetectMultiplexer reports the multiplexer hosting the current session, or an
// empty string when neither TMUX nor STY is set.
func DetectMultiplexer(getenv func(string) string) string {
	if getenv("TMUX") != "" {
		return MultiplexerTmux
	}
	if getenv("STY") != "" {
		return MultiplexerScreen
	}
	return ""
}

// PaneCommand builds the argv that captures the active pane into outPath. The
// tmux capture is written to stdout, so the caller redirects it into outPath.
func PaneCommand(multiplexer string, outPath string) (string, []string, error) {
	switch multiplexer {
	case MultiplexerTmux:
		return "tmux", []string{"capture-pane", "-p", "-S", "-"}, nil
	case MultiplexerScreen:
		return "screen", []string{"-X", "hardcopy", "-h", outPath}, nil
	case "":
		return "", nil, ErrNoMultiplexer
	default:
		return "", nil, fmt.Errorf("%w: %q", ErrUnsupportedMultiplexer, multiplexer)
	}
}

// CapturePane captures the active pane through runner and returns its contents.
// It never reads from stdin: the only sources are the multiplexer commands. A
// missing or empty capture is reported as ErrNoPaneOutput, so callers do not
// send an empty context to a provider.
func CapturePane(multiplexer string, outPath string, runner Runner) (string, error) {
	if runner == nil {
		return "", ErrNoRunner
	}

	name, args, err := PaneCommand(multiplexer, outPath)
	if err != nil {
		return "", err
	}

	if err := runner.RunToFile(name, args, outPath); err != nil {
		return "", fmt.Errorf("terminal: %s capture failed: %w", name, err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: %s wrote no capture file", ErrNoPaneOutput, name)
		}
		return "", fmt.Errorf("terminal: reading pane output: %w", err)
	}

	if strings.TrimSpace(string(data)) == "" {
		return "", fmt.Errorf("%w: %s returned an empty pane", ErrNoPaneOutput, name)
	}

	return string(data), nil
}

// ShellName normalizes a shell path or executable name, or returns "" when the
// shell is not one wut knows how to ask for a prompt.
func ShellName(path string) string {
	if path == "" {
		return ""
	}

	ext := strings.ToLower(filepath.Ext(path))
	if contains(knownShells, ext) {
		return ext
	}

	base := strings.ToLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	if contains(knownShells, base) {
		return base
	}

	if contains(knownShells, strings.ToLower(path)) {
		return strings.ToLower(path)
	}

	return ""
}

func contains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}

// ShellPromptCommand builds the argv that prints the shell prompt, reporting
// false for shells wut cannot interrogate.
func ShellPromptCommand(name string, path string) (string, []string, bool) {
	switch name {
	case "zsh":
		return path, []string{path, "-c", "print -P $PS1"}, true
	case "bash":
		// Parameter transformation; only supported in Bash 4.4+. It is not
		// expanded here because the probe runs without a shell, so the raw
		// expansion is reported back and treated as an unknown prompt.
		return "echo", []string{`"${PS1@P}"`}, true
	case "fish":
		return path, []string{path, "fish_prompt"}, true
	case "csh", "tcsh":
		return path, []string{path, "-c", "echo $prompt"}, true
	case "pwsh", "powershell":
		return path, []string{path, "-c", "Write-Host $prompt"}, true
	default:
		return "", nil, false
	}
}

// ShellPrompt returns the trimmed prompt of the given shell, or "" when it
// cannot be determined. A failed command is not an error: wut degrades to
// prompt-less context.
func ShellPrompt(name string, path string, runner Runner) string {
	if runner == nil {
		return ""
	}

	bin, args, ok := ShellPromptCommand(name, path)
	if !ok {
		return ""
	}

	out, err := runner.Output(bin, args)
	if err != nil {
		return ""
	}

	out = strings.TrimSpace(out)
	// Without a shell the probe cannot expand PS1, so the raw expansion comes
	// back verbatim. Any echo of it means the prompt is unknown.
	if name == "bash" && strings.Contains(out, "${PS1@P}") {
		return ""
	}
	return out
}

// DetectShell resolves the current shell from SHELL or TF_SHELL and queries its
// prompt. An unrecognized path is returned without a prompt.
func DetectShell(getenv func(string) string, runner Runner) Shell {
	path := getenv("SHELL")
	if path == "" {
		path = getenv("TF_SHELL")
	}

	name := ShellName(path)
	if name == "" {
		return Shell{Path: path}
	}

	return Shell{Path: path, Name: name, Prompt: ShellPrompt(name, path, runner)}
}

// DetectShellWithTree resolves the shell from the environment and, when the
// environment does not name a known shell, from the process tree. The
// environment path is preserved so the user still sees what wut detected.
func DetectShellWithTree(getenv func(string) string, runner Runner, startPID int, tree ProcessTree) Shell {
	shell := DetectShell(getenv, runner)
	if shell.Name != "" {
		return shell
	}

	for pid := startPID; pid > 0; {
		path := tree.name(pid)
		if name := ShellName(path); name != "" {
			return Shell{
				Path:   cmp(shell.Path, path),
				Name:   name,
				Prompt: ShellPrompt(name, path, runner),
			}
		}

		parent, ok := tree.parent(pid)
		if !ok {
			break
		}
		pid = parent
	}

	return shell
}

// cmp returns a when it is non-empty, otherwise b.
func cmp(a string, b string) string {
	if a != "" {
		return a
	}
	return b
}

// DetectShellFromProcessTree walks process parents from startPID looking for a
// known shell, mirroring the Python implementation's psutil walk.
func DetectShellFromProcessTree(startPID int, runner Runner, tree ProcessTree) Shell {
	for pid := startPID; pid > 0; {
		path := tree.name(pid)
		if name := ShellName(path); name != "" {
			return Shell{Path: path, Name: name, Prompt: ShellPrompt(name, path, runner)}
		}

		parent, ok := tree.parent(pid)
		if !ok {
			break
		}
		pid = parent
	}

	return Shell{}
}

// ExecRunner runs commands with os/exec.
type ExecRunner struct {
	Stderr *os.File
}

// Output runs the command and returns its stdout.
func (r ExecRunner) Output(name string, args []string) (string, error) {
	cmd := exec.Command(name, args...)
	if r.Stderr != nil {
		cmd.Stderr = r.Stderr
	}
	out, err := cmd.Output()
	return string(out), err
}

// RunToFile runs the command with stdout redirected into outPath.
func (r ExecRunner) RunToFile(name string, args []string, outPath string) error {
	file, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer file.Close()

	cmd := exec.Command(name, args...)
	cmd.Stdout = file
	if r.Stderr != nil {
		cmd.Stderr = r.Stderr
	}
	return cmd.Run()
}
