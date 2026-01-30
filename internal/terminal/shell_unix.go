//go:build !windows

package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/creack/pty"
)

func (s *Session) startShell() (*exec.Cmd, *os.File, error) {
	shell := strings.TrimSpace(s.shell)
	useBash := shell == "" || shell == "bash" || strings.HasSuffix(shell, "/bash")

	var cmd *exec.Cmd
	if useBash {
		rcPath, err := s.ensureBashRC()
		if err != nil {
			return nil, nil, err
		}
		cmd = exec.Command("bash", "--rcfile", rcPath)
	} else {
		cmd = exec.Command(shell)
	}
	cmd.Dir = s.workDir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
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

func (s *Session) ensureBashRC() (string, error) {
	s.mu.Lock()
	rcPath := s.bashRCPath
	s.mu.Unlock()

	if rcPath != "" {
		if _, err := os.Stat(rcPath); err == nil {
			return rcPath, nil
		}
	}

	file, err := os.CreateTemp("", "alices-mirror-bashrc-*")
	if err != nil {
		return "", fmt.Errorf("failed to create bash rc file: %w", err)
	}
	path := file.Name()

	if _, err := file.WriteString(buildBashRC()); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("failed to write bash rc file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("failed to close bash rc file: %w", err)
	}

	s.mu.Lock()
	s.bashRCPath = path
	s.mu.Unlock()

	return path, nil
}

func buildBashRC() string {
	lines := []string{
		"# alices mirror shell title integration",
		"if [ -f /etc/bash.bashrc ]; then . /etc/bash.bashrc; fi",
		"if [ -f ~/.bashrc ]; then . ~/.bashrc; fi",
		"",
		"if [ -z \"${ALICES_MIRROR_PROMPT_INSTALLED:-}\" ]; then",
		"  ALICES_MIRROR_PROMPT_INSTALLED=1",
		"  __alices_mirror_title_prefix='alices-mirror|'",
		"",
		"  __alices_mirror_emit_title() {",
		"    local cwd=\"$1\"",
		"    local proc=\"$2\"",
		"    cwd=${cwd//|/}",
		"    proc=${proc//|/}",
		"    printf '\\033]0;%s%s|%s\\007' \"$__alices_mirror_title_prefix\" \"$cwd\" \"$proc\"",
		"  }",
		"",
		"  __alices_mirror_format_cwd() {",
		"    local cwd=\"$PWD\"",
		"    local home=\"$HOME\"",
		"    if [ -n \"$home\" ] && [[ \"$cwd\" == \"$home\"* ]]; then",
		"      cwd=\"~${cwd#$home}\"",
		"      if [ -z \"$cwd\" ] || [ \"$cwd\" = \"~\" ]; then",
		"        cwd=\"~\"",
		"      fi",
		"    fi",
		"    printf '%s' \"$cwd\"",
		"  }",
		"",
		"  __alices_mirror_set_title() {",
		"    local proc=\"$1\"",
		"    if [ -z \"$proc\" ]; then",
		"      proc=\"bash\"",
		"    fi",
		"    local cwd",
		"    cwd=\"$(__alices_mirror_format_cwd)\"",
		"    __alices_mirror_emit_title \"$cwd\" \"$proc\"",
		"  }",
		"",
		"  __alices_mirror_precmd() {",
		"    __alices_mirror_set_title \"bash\"",
		"  }",
		"",
		"  __alices_mirror_preexec() {",
		"    local cmd=\"$1\"",
		"    if [ -z \"$cmd\" ]; then",
		"      return",
		"    fi",
		"    case \"$cmd\" in",
		"      __alices_mirror_*) return ;;",
		"    esac",
		"    cmd=\"${cmd#\"${cmd%%[![:space:]]*}\"}\"",
		"    cmd=\"${cmd%%[[:space:]]*}\"",
		"    if [ -z \"$cmd\" ]; then",
		"      return",
		"    fi",
		"    if [ \"$cmd\" = \"sudo\" ]; then",
		"      local rest=\"${1#sudo }\"",
		"      rest=\"${rest#\"${rest%%[![:space:]]*}\"}\"",
		"      if [ -n \"$rest\" ]; then",
		"        cmd=\"${rest%%[[:space:]]*}\"",
		"      fi",
		"    fi",
		"    __alices_mirror_set_title \"$cmd\"",
		"  }",
		"",
		"  __alices_mirror_prev_debug=$(trap -p DEBUG)",
		"  if [ -n \"$__alices_mirror_prev_debug\" ]; then",
		"    __alices_mirror_prev_debug=${__alices_mirror_prev_debug#*\\'}",
		"    __alices_mirror_prev_debug=${__alices_mirror_prev_debug%\\' DEBUG}",
		"  fi",
		"  __alices_mirror_debug_trap() {",
		"    if [ -n \"$__alices_mirror_prev_debug\" ]; then",
		"      eval \"$__alices_mirror_prev_debug\"",
		"    fi",
		"    __alices_mirror_preexec \"$BASH_COMMAND\"",
		"  }",
		"",
		"  trap '__alices_mirror_debug_trap' DEBUG",
		"  if [ -n \"${PROMPT_COMMAND:-}\" ]; then",
		"    case \";$PROMPT_COMMAND;\" in",
		"      *\";__alices_mirror_precmd;\"*) ;;",
		"      *) PROMPT_COMMAND=\"${PROMPT_COMMAND};__alices_mirror_precmd\" ;;",
		"    esac",
		"  else",
		"    PROMPT_COMMAND=\"__alices_mirror_precmd\"",
		"  fi",
		"fi",
		"",
	}

	return strings.Join(lines, "\n")
}
