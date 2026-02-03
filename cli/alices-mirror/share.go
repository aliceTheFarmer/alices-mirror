package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"

	"github.com/gorilla/websocket"

	"alices-mirror/internal/app"
	"alices-mirror/internal/server"
)

const (
	shareOwnerTokenEnv = "ALICES_MIRROR_OWNER_TOKEN"
	titlePrefixEnv     = "ALICES_MIRROR_TITLE_PREFIX"
)

func runShare(cfg app.Config, canonical []string, workDir string, cwdProvided bool) error {
	if err := app.Validate(cfg); err != nil {
		return err
	}

	ownerToken, err := newOwnerToken()
	if err != nil {
		return err
	}

	titlePrefix := fmt.Sprintf("alices-mirror(shared:%d)", cfg.Port)
	args := shareDaemonArgs(canonical, workDir, cwdProvided)

	restoreEnv, err := withEnv(map[string]string{
		shareOwnerTokenEnv: ownerToken,
		titlePrefixEnv:     titlePrefix,
	})
	if err != nil {
		return err
	}
	pid, err := startDaemon(args)
	restoreEnv()
	if err != nil {
		return fmt.Errorf("failed to start daemon: %v", err)
	}

	auth := app.BuildAuthConfig(cfg)
	lines := app.StartupLines(app.StartupInfo{
		WorkDir: cfg.WorkDir,
		Port:    cfg.Port,
		Origins: cfg.Origins,
		Auth:    auth,
		PID:     pid,
		Daemon:  true,
	})
	for _, line := range lines {
		fmt.Println(line)
	}
	fmt.Printf("This terminal is now attached to the shared shell (port %d).\n", cfg.Port)
	fmt.Println("Close the shell (exit / Ctrl+D) to stop the server.")
	fmt.Println()

	if err := attachOwnerShell(cfg, ownerToken); err != nil {
		_ = killProcess(pid)
		return err
	}

	return nil
}

func shareDaemonArgs(canonical []string, workDir string, cwdProvided bool) []string {
	args := daemonArgs(canonical, workDir, cwdProvided)
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--share" {
			continue
		}
		out = append(out, arg)
	}
	return out
}

func attachOwnerShell(cfg app.Config, ownerToken string) error {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return errors.New("--share requires an interactive terminal on stdin")
	}

	binds := server.ExpandBindPatterns(cfg.Origins)
	if len(binds) == 0 {
		binds = cfg.Origins
	}
	ownerURL, err := buildOwnerWSURL(binds, cfg.Port, ownerToken)
	if err != nil {
		return err
	}

	header := http.Header{}
	auth := app.BuildAuthConfig(cfg)
	if auth.Enabled {
		header.Set("Authorization", basicAuthHeader(auth.User, auth.Password))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	conn, err := dialWebsocketWithRetry(ctx, ownerURL, header)
	if err != nil {
		return err
	}
	defer conn.Close()

	prevState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("failed to set terminal raw mode: %v", err)
	}
	defer term.Restore(fd, prevState)

	writer := &wsWriter{conn: conn}

	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := os.Stdin.Read(buf)
			if n > 0 {
				if writeErr := writer.WriteBinary(buf[:n]); writeErr != nil {
					return
				}
			}
			if readErr != nil {
				return
			}
		}
	}()

	go func() {
		sendResizeLoop(writer)
	}()

	for {
		messageType, payload, readErr := conn.ReadMessage()
		if readErr != nil {
			return nil
		}
		switch messageType {
		case websocket.BinaryMessage:
			_, _ = os.Stdout.Write(payload)
		case websocket.TextMessage:
			// Intentionally ignore control/status messages to avoid corrupting the interactive display.
		}
	}
}

type wsWriter struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func (w *wsWriter) WriteBinary(p []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteMessage(websocket.BinaryMessage, p)
}

func (w *wsWriter) WriteJSON(v any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteJSON(v)
}

func sendResizeLoop(writer *wsWriter) {
	type controlMessage struct {
		Type string `json:"type"`
		Cols int    `json:"cols"`
		Rows int    `json:"rows"`
	}

	lastCols := -1
	lastRows := -1

	for {
		cols, rows, err := term.GetSize(int(os.Stdout.Fd()))
		if err == nil && cols > 0 && rows > 0 && (cols != lastCols || rows != lastRows) {
			_ = writer.WriteJSON(controlMessage{Type: "resize", Cols: cols, Rows: rows})
			lastCols = cols
			lastRows = rows
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func buildOwnerWSURL(origins []string, port int, ownerToken string) (string, error) {
	host := chooseLocalHost(origins)
	if host == "" {
		return "", errors.New("no origin host available for owner connection")
	}

	address := net.JoinHostPort(host, strconv.Itoa(port))
	u := url.URL{
		Scheme: "ws",
		Host:   address,
		Path:   "/ws-owner",
	}
	q := u.Query()
	q.Set("token", ownerToken)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func chooseLocalHost(origins []string) string {
	var first string
	hasAny := false
	hasBindAll := false
	hasIPv6All := false
	for _, origin := range origins {
		cleaned := strings.TrimSpace(origin)
		if cleaned == "" {
			continue
		}
		if !hasAny {
			first = cleaned
			hasAny = true
		}
		switch strings.ToLower(cleaned) {
		case "127.0.0.1":
			return "127.0.0.1"
		case "localhost":
			return "127.0.0.1"
		case "::1":
			return "::1"
		case "0.0.0.0":
			hasBindAll = true
		case "::":
			hasIPv6All = true
		}
	}
	if hasBindAll || hasIPv6All {
		return "127.0.0.1"
	}
	return first
}

func dialWebsocketWithRetry(ctx context.Context, wsURL string, header http.Header) (*websocket.Conn, error) {
	deadline, hasDeadline := ctx.Deadline()
	backoff := 150 * time.Millisecond

	for {
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
		if err == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			if hasDeadline && time.Now().After(deadline) {
				return nil, fmt.Errorf("failed to connect to owner session: %v", err)
			}
			return nil, err
		}
		time.Sleep(backoff)
		if backoff < 500*time.Millisecond {
			backoff += 50 * time.Millisecond
		}
	}
}

func basicAuthHeader(user, password string) string {
	token := base64.StdEncoding.EncodeToString([]byte(user + ":" + password))
	return "Basic " + token
}

func newOwnerToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func withEnv(values map[string]string) (func(), error) {
	type prevValue struct {
		value string
		ok    bool
	}

	prev := make(map[string]prevValue, len(values))
	for key := range values {
		val, ok := os.LookupEnv(key)
		prev[key] = prevValue{value: val, ok: ok}
	}

	for key, value := range values {
		if err := os.Setenv(key, value); err != nil {
			return nil, err
		}
	}

	return func() {
		for key, item := range prev {
			if item.ok {
				_ = os.Setenv(key, item.value)
				continue
			}
			_ = os.Unsetenv(key)
		}
	}, nil
}

func killProcess(pid int) error {
	if pid <= 0 {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}
