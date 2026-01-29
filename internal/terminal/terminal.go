package terminal

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
)

type Config struct {
	WorkDir    string
	BufferSize int
	Shell      string
}

type Session struct {
	mu       sync.Mutex
	ptyFile  *os.File
	cmd      *exec.Cmd
	workDir  string
	shell    string
	buffer   *ringBuffer
	outputCh chan []byte
	statusCh chan string
	lastCols int
	lastRows int
	writeMu  sync.Mutex
}

func NewSession(cfg Config) (*Session, error) {
	if cfg.WorkDir == "" {
		return nil, errors.New("work directory is required")
	}
	bufferSize := cfg.BufferSize
	if bufferSize <= 0 {
		bufferSize = 256 * 1024
	}

	s := &Session{
		workDir:  cfg.WorkDir,
		shell:    cfg.Shell,
		buffer:   newRingBuffer(bufferSize),
		outputCh: make(chan []byte, 128),
		statusCh: make(chan string, 16),
	}

	go s.runLoop()
	return s, nil
}

func (s *Session) Output() <-chan []byte {
	return s.outputCh
}

func (s *Session) Status() <-chan string {
	return s.statusCh
}

func (s *Session) Snapshot() []byte {
	return s.buffer.Bytes()
}

func (s *Session) WriteInput(data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.mu.Lock()
	ptyFile := s.ptyFile
	s.mu.Unlock()

	if ptyFile == nil {
		return errors.New("shell not ready")
	}

	_, err := ptyFile.Write(data)
	return err
}

func (s *Session) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return errors.New("invalid terminal size")
	}

	s.mu.Lock()
	s.lastCols = cols
	s.lastRows = rows
	ptyFile := s.ptyFile
	s.mu.Unlock()

	if ptyFile == nil {
		return nil
	}

	return pty.Setsize(ptyFile, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (s *Session) runLoop() {
	for {
		cmd, ptyFile, err := s.startShell()
		if err != nil {
			s.emitStatus(fmt.Sprintf("Shell start failed: %v", err))
			time.Sleep(2 * time.Second)
			continue
		}

		s.setPTY(cmd, ptyFile)
		s.emitStatus("Shell started.")

		done := make(chan error, 1)
		go func() {
			done <- cmd.Wait()
		}()

		s.readLoop(ptyFile)
		_ = ptyFile.Close()
		<-done

		s.emitStatus("Shell exited. Respawning now.")
		s.clearPTY()
	}
}

func (s *Session) readLoop(ptyFile *os.File) {
	buf := make([]byte, 4096)
	for {
		n, err := ptyFile.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.buffer.Append(chunk)
			s.emitOutput(chunk)
		}
		if err != nil {
			return
		}
	}
}

func (s *Session) emitOutput(data []byte) {
	select {
	case s.outputCh <- data:
	default:
	}
}

func (s *Session) emitStatus(message string) {
	select {
	case s.statusCh <- message:
	default:
	}
}

func (s *Session) setPTY(cmd *exec.Cmd, ptyFile *os.File) {
	s.mu.Lock()
	s.cmd = cmd
	s.ptyFile = ptyFile
	s.mu.Unlock()
}

func (s *Session) clearPTY() {
	s.mu.Lock()
	s.cmd = nil
	s.ptyFile = nil
	s.mu.Unlock()
}

// ringBuffer keeps the last N bytes of output for new clients.
type ringBuffer struct {
	mu   sync.Mutex
	data []byte
	max  int
}

func newRingBuffer(max int) *ringBuffer {
	return &ringBuffer{max: max}
}

func (r *ringBuffer) Append(p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(p) >= r.max {
		r.data = append(r.data[:0], p[len(p)-r.max:]...)
		return
	}

	needed := len(r.data) + len(p) - r.max
	if needed > 0 {
		r.data = r.data[needed:]
	}

	r.data = append(r.data, p...)
}

func (r *ringBuffer) Bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	copyBuf := make([]byte, len(r.data))
	copy(copyBuf, r.data)
	return copyBuf
}
