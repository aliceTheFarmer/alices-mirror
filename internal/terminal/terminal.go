package terminal

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

type Config struct {
	WorkDir         string
	BufferSize      int
	Shell           string
	ExitOnShellExit bool
}

type Session struct {
	mu              sync.Mutex
	pty             ptyDevice
	cmd             shellCommand
	workDir         string
	shell           string
	bashRCPath      string
	exitOnShellExit bool
	buffer          *ringBuffer
	outputCh        chan []byte
	statusCh        chan string
	doneCh          chan struct{}
	lastCols        int
	lastRows        int
	lastTitleCwd    string
	lastTitleProc   string
	writeMu         sync.Mutex
	closeOnce       sync.Once
	closed          bool
}

type ptyDevice interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
}

type shellCommand interface {
	PID() int
	Kill() error
	Wait() error
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
		workDir:         cfg.WorkDir,
		shell:           cfg.Shell,
		exitOnShellExit: cfg.ExitOnShellExit,
		buffer:          newRingBuffer(bufferSize),
		outputCh:        make(chan []byte, 128),
		statusCh:        make(chan string, 16),
		doneCh:          make(chan struct{}),
	}

	go s.runLoop()
	return s, nil
}

func CheckShell(workDir, shell string) error {
	s := &Session{
		workDir: workDir,
		shell:   shell,
	}
	cmd, ptyHandle, err := s.startShell()
	if err != nil {
		return err
	}
	_ = ptyHandle.Close()
	_ = cmd.Kill()
	_ = cmd.Wait()
	return nil
}

func (s *Session) Output() <-chan []byte {
	return s.outputCh
}

func (s *Session) Status() <-chan string {
	return s.statusCh
}

func (s *Session) Done() <-chan struct{} {
	return s.doneCh
}

func (s *Session) Snapshot() []byte {
	return s.buffer.Bytes()
}

func (s *Session) WriteInput(data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.mu.Lock()
	ptyHandle := s.pty
	s.mu.Unlock()

	if ptyHandle == nil {
		return errors.New("shell not ready")
	}

	_, err := ptyHandle.Write(data)
	return err
}

func (s *Session) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return errors.New("invalid terminal size")
	}

	s.mu.Lock()
	s.lastCols = cols
	s.lastRows = rows
	ptyHandle := s.pty
	s.mu.Unlock()

	if ptyHandle == nil {
		return nil
	}

	return ptyHandle.Resize(cols, rows)
}

func (s *Session) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	cmd := s.cmd
	ptyHandle := s.pty
	s.mu.Unlock()

	if ptyHandle != nil {
		_ = ptyHandle.Close()
	}
	if cmd != nil {
		_ = cmd.Kill()
	}
}

func (s *Session) runLoop() {
	for {
		if s.isClosed() {
			s.closeChannels()
			return
		}
		cmd, ptyHandle, err := s.startShell()
		if err != nil {
			s.emitStatus(fmt.Sprintf("Shell start failed: %v", err))
			time.Sleep(2 * time.Second)
			continue
		}

		s.setPTY(cmd, ptyHandle)
		s.emitStatus("Shell started.")

		done := make(chan error, 1)
		go func() {
			done <- cmd.Wait()
		}()

		s.readLoop(ptyHandle)
		_ = ptyHandle.Close()
		<-done

		s.clearPTY()
		if s.isClosed() {
			s.closeChannels()
			return
		}
		if s.exitOnShellExit {
			s.emitStatus("Shell exited.")
			s.mu.Lock()
			s.closed = true
			s.mu.Unlock()
			s.closeChannels()
			return
		}
		s.emitStatus("Shell exited. Respawning now.")
	}
}

func (s *Session) readLoop(reader io.Reader) {
	parser := newOSCTitleParser()
	buf := make([]byte, 4096)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			for _, title := range parser.Feed(chunk) {
				s.captureTitle(title)
			}
			s.buffer.Append(chunk)
			s.emitOutput(chunk)
		}
		if err != nil {
			return
		}
	}
}

func (s *Session) captureTitle(title string) {
	cwd, proc, ok := parseAlicesMirrorTitle(title)
	if !ok {
		return
	}
	if cwd == "" && proc == "" {
		return
	}
	s.mu.Lock()
	if cwd != "" {
		s.lastTitleCwd = cwd
	}
	if proc != "" {
		s.lastTitleProc = proc
	}
	s.mu.Unlock()
}

func (s *Session) emitOutput(data []byte) {
	if s.isClosed() {
		return
	}
	select {
	case s.outputCh <- data:
	default:
	}
}

func (s *Session) emitStatus(message string) {
	if s.isClosed() {
		return
	}
	select {
	case s.statusCh <- message:
	default:
	}
}

func (s *Session) setPTY(cmd shellCommand, ptyHandle ptyDevice) {
	s.mu.Lock()
	s.cmd = cmd
	s.pty = ptyHandle
	s.mu.Unlock()
}

func (s *Session) clearPTY() {
	s.mu.Lock()
	s.cmd = nil
	s.pty = nil
	s.mu.Unlock()
}

func (s *Session) isClosed() bool {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	return closed
}

func (s *Session) closeChannels() {
	s.closeOnce.Do(func() {
		close(s.outputCh)
		close(s.statusCh)
		close(s.doneCh)
	})
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
