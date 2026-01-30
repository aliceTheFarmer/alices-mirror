package terminal

import "errors"

type ProcessInfo struct {
	PID  int    `json:"pid"`
	Name string `json:"name"`
}

func (s *Session) Reset() ([]ProcessInfo, error) {
	s.mu.Lock()
	cmd := s.cmd
	ptyFile := s.ptyFile
	s.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return nil, errors.New("shell not ready")
	}

	if ptyFile != nil {
		_ = ptyFile.Close()
	}

	return terminateProcessTree(cmd)
}
