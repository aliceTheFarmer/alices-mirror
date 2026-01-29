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
		cmd = exec.Command("powershell.exe")
	case "cmd":
		cmd = exec.Command("cmd.exe")
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
