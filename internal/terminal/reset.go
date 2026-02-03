package terminal

import "errors"

type ProcessInfo struct {
	PID  int    `json:"pid"`
	Name string `json:"name"`
}

func (s *Session) Reset() ([]ProcessInfo, error) {
	s.mu.Lock()
	cmd := s.cmd
	ptyHandle := s.pty
	s.mu.Unlock()

	if cmd == nil || cmd.PID() <= 0 {
		return nil, errors.New("shell not ready")
	}

	if ptyHandle != nil {
		_ = ptyHandle.Close()
	}

	return terminateProcessTree(cmd.PID())
}
