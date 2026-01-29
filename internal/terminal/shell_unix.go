//go:build !windows

package terminal

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

func (s *Session) startShell() (*exec.Cmd, *os.File, error) {
	cmd := exec.Command("bash")
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
