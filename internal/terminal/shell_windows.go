//go:build windows

package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/creack/pty"
)

func (s *Session) startShell() (*exec.Cmd, *os.File, error) {
	shell := strings.ToLower(strings.TrimSpace(s.shell))
	if shell == "" {
		shell = "powershell"
	}

	var cmd *exec.Cmd
	switch shell {
	case "powershell":
		cmd = exec.Command("powershell.exe", "-NoLogo", "-NoExit", "-Command", buildPowerShellInitScript())
	case "cmd":
		cmd = exec.Command("cmd.exe", "/Q", "/V:ON", "/K", buildCmdInitCommand())
	default:
		return nil, nil, fmt.Errorf("unsupported shell %q", s.shell)
	}

	cmd.Dir = s.workDir
	cmd.Env = os.Environ()

	ptyFile, err := pty.Start(cmd)
	if err != nil {
		return nil, nil, err
	}

	s.mu.Lock()
	cols := s.lastCols
	rows := s.lastRows
	s.mu.Unlock()

	if cols > 0 && rows > 0 {
		_ = pty.Setsize(ptyFile, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	}

	return cmd, ptyFile, nil
}

func buildCmdInitCommand() string {
	return "prompt $E]0;alices-mirror^|$P^|cmd$E\\%PROMPT%"
}

func buildPowerShellInitScript() string {
	lines := []string{
		"$ErrorActionPreference = 'SilentlyContinue'",
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
		"  [Console]::Write(\"`e]0;alices-mirror|$safeCwd|$safeProc`a\")",
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
