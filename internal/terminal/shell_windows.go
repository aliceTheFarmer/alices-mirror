//go:build windows

package terminal

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

type conPTYDevice struct {
	mu      sync.Mutex
	console windows.Handle
	inPipe  *os.File
	outPipe *os.File
	closed  bool
}

func newConPTYDevice(cols, rows int) (*conPTYDevice, error) {
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 25
	}
	cols16 := clampInt16(cols)
	rows16 := clampInt16(rows)

	consoleIn, inPipeOurs, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create pseudo console pipes: %w", err)
	}

	outPipeOurs, consoleOut, err := os.Pipe()
	if err != nil {
		_ = consoleIn.Close()
		_ = inPipeOurs.Close()
		return nil, fmt.Errorf("failed to create pseudo console pipes: %w", err)
	}

	var console windows.Handle
	coord := windows.Coord{X: cols16, Y: rows16}
	if err := windows.CreatePseudoConsole(coord, windows.Handle(consoleIn.Fd()), windows.Handle(consoleOut.Fd()), 0, &console); err != nil {
		if errors.Is(err, windows.ERROR_CALL_NOT_IMPLEMENTED) || errors.Is(err, windows.ERROR_PROC_NOT_FOUND) {
			err = fmt.Errorf("ConPTY is not supported on this Windows version: %w", err)
		}
		_ = consoleIn.Close()
		_ = inPipeOurs.Close()
		_ = outPipeOurs.Close()
		_ = consoleOut.Close()
		return nil, fmt.Errorf("failed to create pseudo console: %w", err)
	}

	_ = consoleIn.Close()
	_ = consoleOut.Close()

	return &conPTYDevice{
		console: console,
		inPipe:  inPipeOurs,
		outPipe: outPipeOurs,
	}, nil
}

func clampInt16(value int) int16 {
	if value < 1 {
		return 1
	}
	if value > 32767 {
		return 32767
	}
	return int16(value)
}

func (p *conPTYDevice) Read(buf []byte) (int, error) {
	p.mu.Lock()
	out := p.outPipe
	p.mu.Unlock()
	if out == nil {
		return 0, errors.New("pseudo console closed")
	}
	return out.Read(buf)
}

func (p *conPTYDevice) Write(buf []byte) (int, error) {
	p.mu.Lock()
	in := p.inPipe
	p.mu.Unlock()
	if in == nil {
		return 0, errors.New("pseudo console closed")
	}
	return in.Write(buf)
}

func (p *conPTYDevice) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	console := p.console
	p.console = 0
	in := p.inPipe
	out := p.outPipe
	p.inPipe = nil
	p.outPipe = nil
	p.mu.Unlock()

	if console != 0 {
		windows.ClosePseudoConsole(console)
	}
	return errors.Join(closeFile(in), closeFile(out))
}

func closeFile(file *os.File) error {
	if file == nil {
		return nil
	}
	return file.Close()
}

func (p *conPTYDevice) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	p.mu.Lock()
	console := p.console
	p.mu.Unlock()
	if console == 0 {
		return nil
	}
	coord := windows.Coord{X: clampInt16(cols), Y: clampInt16(rows)}
	if err := windows.ResizePseudoConsole(console, coord); err != nil {
		return fmt.Errorf("failed to resize pseudo console: %w", err)
	}
	return nil
}

type windowsShellCommand struct {
	mu     sync.Mutex
	pid    int
	handle windows.Handle
}

func (c *windowsShellCommand) PID() int {
	if c == nil {
		return 0
	}
	return c.pid
}

func (c *windowsShellCommand) Kill() error {
	c.mu.Lock()
	handle := c.handle
	c.mu.Unlock()
	if handle == 0 {
		return nil
	}
	return windows.TerminateProcess(handle, 1)
}

func (c *windowsShellCommand) Wait() error {
	c.mu.Lock()
	handle := c.handle
	c.mu.Unlock()
	if handle == 0 {
		return nil
	}

	_, err := windows.WaitForSingleObject(handle, windows.INFINITE)
	if err != nil {
		return err
	}

	c.mu.Lock()
	if c.handle != 0 {
		_ = windows.CloseHandle(c.handle)
		c.handle = 0
	}
	c.mu.Unlock()
	return nil
}

func (s *Session) startShell() (shellCommand, ptyDevice, error) {
	shell := strings.ToLower(strings.TrimSpace(s.shell))
	if shell == "" {
		shell = "powershell"
	}

	s.mu.Lock()
	cols := s.lastCols
	rows := s.lastRows
	s.mu.Unlock()

	ptyHandle, err := newConPTYDevice(cols, rows)
	if err != nil {
		return nil, nil, err
	}

	exe, args, err := windowsShellCommandLine(shell)
	if err != nil {
		_ = ptyHandle.Close()
		return nil, nil, err
	}

	process, err := startAttachedProcess(exe, args, s.workDir, dropEnvVar(os.Environ(), "ALICES_MIRROR_OWNER_TOKEN"), ptyHandle.console)
	if err != nil {
		_ = ptyHandle.Close()
		return nil, nil, err
	}

	return &windowsShellCommand{pid: process.pid, handle: process.handle}, ptyHandle, nil
}

type startedWindowsProcess struct {
	pid    int
	handle windows.Handle
}

func windowsShellCommandLine(shell string) (string, []string, error) {
	switch shell {
	case "powershell":
		return "powershell.exe", []string{"-NoLogo", "-NoExit", "-Command", buildPowerShellInitScript()}, nil
	case "cmd":
		return "cmd.exe", []string{"/Q", "/V:ON", "/K", buildCmdInitCommand()}, nil
	default:
		return "", nil, fmt.Errorf("unsupported shell %q", shell)
	}
}

func startAttachedProcess(exe string, args []string, workDir string, env []string, console windows.Handle) (*startedWindowsProcess, error) {
	exePath, err := exec.LookPath(exe)
	if err != nil {
		return nil, err
	}
	argv0, err := windows.UTF16PtrFromString(exePath)
	if err != nil {
		return nil, err
	}

	cmdArgs := append([]string{exePath}, args...)
	cmdLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(cmdArgs))
	if err != nil {
		return nil, err
	}

	var dirPtr *uint16
	if strings.TrimSpace(workDir) != "" {
		dirPtr, err = windows.UTF16PtrFromString(workDir)
		if err != nil {
			return nil, err
		}
	}

	envBlock, err := createEnvBlock(env)
	if err != nil {
		return nil, err
	}
	envPtr := &envBlock[0]

	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize process thread attribute list: %w", err)
	}
	defer attrs.Delete()

	if err := attrs.Update(
		windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		unsafe.Pointer(console),
		unsafe.Sizeof(console),
	); err != nil {
		return nil, fmt.Errorf("failed to attach pseudo console: %w", err)
	}

	siEx := new(windows.StartupInfoEx)
	siEx.ProcThreadAttributeList = attrs.List()
	siEx.Cb = uint32(unsafe.Sizeof(*siEx))

	pi := new(windows.ProcessInformation)
	flags := uint32(windows.CREATE_UNICODE_ENVIRONMENT) | windows.EXTENDED_STARTUPINFO_PRESENT
	if err := windows.CreateProcess(argv0, cmdLine, nil, nil, false, flags, envPtr, dirPtr, &siEx.StartupInfo, pi); err != nil {
		return nil, fmt.Errorf("failed to create process: %w", err)
	}

	_ = windows.CloseHandle(pi.Thread)

	return &startedWindowsProcess{pid: int(pi.ProcessId), handle: pi.Process}, nil
}

func createEnvBlock(env []string) ([]uint16, error) {
	if env == nil {
		env = os.Environ()
	}

	cleaned := make([]string, 0, len(env))
	for _, item := range env {
		if item == "" {
			continue
		}
		if strings.ContainsRune(item, '\x00') {
			return nil, errors.New("environment contains NUL")
		}
		cleaned = append(cleaned, item)
	}

	sort.Slice(cleaned, func(i, j int) bool {
		return strings.ToLower(cleaned[i]) < strings.ToLower(cleaned[j])
	})

	var block []uint16
	for _, item := range cleaned {
		encoded, err := windows.UTF16FromString(item)
		if err != nil {
			return nil, err
		}
		block = append(block, encoded...)
	}
	block = append(block, 0)
	if len(block) == 1 {
		block = append(block, 0)
	}
	return block, nil
}

func buildCmdInitCommand() string {
	return "if \"%ALICES_MIRROR_TITLE_PREFIX%\"==\"\" set \"ALICES_MIRROR_TITLE_PREFIX=alices-mirror\" & prompt $E]0;%ALICES_MIRROR_TITLE_PREFIX%^|$P^|cmd$E\\%PROMPT%"
}

func buildPowerShellInitScript() string {
	lines := []string{
		"$ErrorActionPreference = 'SilentlyContinue'",
		"$script:__AlicesMirrorTitlePrefix = $env:ALICES_MIRROR_TITLE_PREFIX",
		"if (-not $script:__AlicesMirrorTitlePrefix) { $script:__AlicesMirrorTitlePrefix = 'alices-mirror' }",
		"$script:__AlicesMirrorTitlePrefix = $script:__AlicesMirrorTitlePrefix.Replace('|', '')",
		"function global:__AlicesMirrorFormatCwd {",
		"  $cwd = (Get-Location).Path",
		"  $home = $HOME",
		"  if ($home -and $cwd.StartsWith($home, [System.StringComparison]::OrdinalIgnoreCase)) {",
		"    $suffix = $cwd.Substring($home.Length)",
		"    if ($suffix) { return \"~$suffix\" }",
		"    return \"~\"",
		"  }",
		"  return $cwd",
		"}",
		"function global:__AlicesMirrorEmitTitle([string]$cwd, [string]$proc) {",
		"  if (-not $cwd) { $cwd = '' }",
		"  if (-not $proc) { $proc = '' }",
		"  $safeCwd = $cwd.Replace('|', '')",
		"  $safeProc = $proc.Replace('|', '')",
		"  $safePrefix = $script:__AlicesMirrorTitlePrefix",
		"  [Console]::Write(\"`e]0;$safePrefix|$safeCwd|$safeProc`a\")",
		"}",
		"function global:__AlicesMirrorSetTitle([string]$proc) {",
		"  $cwd = __AlicesMirrorFormatCwd",
		"  __AlicesMirrorEmitTitle $cwd $proc",
		"}",
		"$script:__AlicesMirrorOriginalPrompt = $function:prompt",
		"function global:prompt {",
		"  __AlicesMirrorSetTitle 'powershell'",
		"  if ($script:__AlicesMirrorOriginalPrompt) { & $script:__AlicesMirrorOriginalPrompt } else { \"PS $pwd> \" }",
		"}",
		"if (Get-Module -ListAvailable -Name PSReadLine) {",
		"  Import-Module PSReadLine -ErrorAction SilentlyContinue",
		"  Set-PSReadLineOption -CommandValidationHandler {",
		"    param($command)",
		"    if ($command) {",
		"      $cmdName = $command.Trim().Split()[0]",
		"      if ($cmdName) { __AlicesMirrorSetTitle $cmdName }",
		"    }",
		"    return $true",
		"  }",
		"}",
	}
	return strings.Join(lines, "\n")
}
