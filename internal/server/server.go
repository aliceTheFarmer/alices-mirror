package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"alices-mirror/internal/terminal"
)

type AuthConfig struct {
	Enabled  bool
	User     string
	Password string
}

type Config struct {
	Addrs      []string
	Session    *terminal.Session
	Auth       AuthConfig
	Alias      string
	OwnerToken string
}

type Server struct {
	addrs      []string
	session    *terminal.Session
	auth       AuthConfig
	alias      string
	ownerToken string

	clientsMu sync.Mutex
	clients   map[*client]struct{}

	ownerMu        sync.Mutex
	ownerConnected bool

	shutdownOnce sync.Once
	shutdownFunc func()
}

type client struct {
	conn    *websocket.Conn
	send    chan wsMessage
	isOwner bool
}

type wsMessage struct {
	messageType int
	data        []byte
}

type controlMessage struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

//go:embed web/* web/vendor/*
var webFS embed.FS

func New(cfg Config) (*Server, error) {
	if cfg.Session == nil {
		return nil, errors.New("session is required")
	}
	if len(cfg.Addrs) == 0 {
		return nil, errors.New("addrs are required")
	}

	addrs := make([]string, 0, len(cfg.Addrs))
	for _, addr := range cfg.Addrs {
		trimmed := strings.TrimSpace(addr)
		if trimmed == "" {
			return nil, errors.New("addr is required")
		}
		addrs = append(addrs, trimmed)
	}
	addrs = uniqueStrings(addrs)
	if len(addrs) == 0 {
		return nil, errors.New("addrs are required")
	}

	s := &Server{
		addrs:      addrs,
		session:    cfg.Session,
		auth:       cfg.Auth,
		alias:      cfg.Alias,
		ownerToken: strings.TrimSpace(cfg.OwnerToken),
		clients:    make(map[*client]struct{}),
	}

	return s, nil
}

func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.Handle("/ws", s.authMiddleware(http.HandlerFunc(s.handleWS)))
	if s.ownerToken != "" {
		mux.Handle("/ws-owner", s.authMiddleware(http.HandlerFunc(s.handleWSOwner)))
	}
	mux.Handle("/", s.authMiddleware(s.staticHandler()))

	srv := &http.Server{
		Addr:              s.addrs[0],
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	listeners, err := listenAll(s.addrs)
	if err != nil {
		return err
	}

	go s.broadcastOutput()
	go s.broadcastStatus()

	errCh := make(chan error, len(listeners))
	for _, listener := range listeners {
		go func(listener net.Listener) {
			errCh <- srv.Serve(listener)
		}(listener)
	}

	shutdown := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}
	s.shutdownFunc = shutdown

	go func() {
		select {
		case <-ctx.Done():
		case <-s.session.Done():
			s.requestShutdown()
		}
	}()

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdown()
		case <-done:
		}
	}()
	defer close(done)

	var serveErr error
	for i := 0; i < len(listeners); i++ {
		err := <-errCh
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			continue
		}
		if serveErr == nil {
			serveErr = err
			shutdown()
		}
	}

	if serveErr != nil {
		return serveErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return http.ErrServerClosed
}

func listenAll(addrs []string) ([]net.Listener, error) {
	listeners := make([]net.Listener, 0, len(addrs))
	for _, addr := range addrs {
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			for _, opened := range listeners {
				_ = opened.Close()
			}
			return nil, fmt.Errorf("failed to listen on %s: %w", addr, err)
		}
		listeners = append(listeners, listener)
	}
	return listeners, nil
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	s.handleWSWithOwnerFlag(w, r, false)
}

func (s *Server) handleWSOwner(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" || token != s.ownerToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	s.ownerMu.Lock()
	if s.ownerConnected {
		s.ownerMu.Unlock()
		http.Error(w, "Owner already connected", http.StatusConflict)
		return
	}
	s.ownerConnected = true
	s.ownerMu.Unlock()

	s.handleWSWithOwnerFlag(w, r, true)
}

func (s *Server) handleWSWithOwnerFlag(w http.ResponseWriter, r *http.Request, isOwner bool) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		if isOwner {
			s.ownerMu.Lock()
			s.ownerConnected = false
			s.ownerMu.Unlock()
		}
		return
	}

	c := &client{
		conn:    conn,
		send:    make(chan wsMessage, 128),
		isOwner: isOwner,
	}

	s.addClient(c)

	snapshot := s.session.Snapshot()
	if len(snapshot) > 0 {
		c.send <- wsMessage{messageType: websocket.BinaryMessage, data: snapshot}
	}

	go c.writePump(s)
	c.readPump(s)
}

func (c *client) writePump(s *Server) {
	defer func() {
		c.conn.Close()
	}()

	for msg := range c.send {
		if err := c.conn.WriteMessage(msg.messageType, msg.data); err != nil {
			return
		}
	}
}

func (c *client) readPump(s *Server) {
	defer func() {
		s.removeClient(c)
		close(c.send)
		c.conn.Close()
		if c.isOwner {
			s.requestShutdown()
		}
	}()

	for {
		messageType, payload, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		switch messageType {
		case websocket.BinaryMessage:
			_ = s.session.WriteInput(payload)
		case websocket.TextMessage:
			var control controlMessage
			if err := json.Unmarshal(payload, &control); err != nil {
				continue
			}
			s.handleControl(control)
		}
	}
}

func (s *Server) requestShutdown() {
	s.shutdownOnce.Do(func() {
		s.session.Close()
		if s.shutdownFunc != nil {
			s.shutdownFunc()
		}
	})
}

func (s *Server) handleControl(control controlMessage) {
	switch control.Type {
	case "resize":
		_ = s.session.Resize(control.Cols, control.Rows)
	case "reset":
		remaining, err := s.session.Reset()
		if err != nil || len(remaining) > 0 {
			s.broadcastResetFailure(remaining, err)
		}
	}
}

func (s *Server) broadcastResetFailure(remaining []terminal.ProcessInfo, err error) {
	title := "Reset failed"
	lines := []string{"The shell could not be fully reset."}
	if err != nil {
		lines = append(lines, fmt.Sprintf("Reason: %s", err.Error()))
	}
	if len(remaining) > 0 {
		lines = append(lines, "The following processes could not be terminated:")
		for _, proc := range remaining {
			name := strings.TrimSpace(proc.Name)
			if name == "" {
				name = "unknown"
			}
			lines = append(lines, fmt.Sprintf("PID %d - %s", proc.PID, name))
		}
	}

	payload, _ := json.Marshal(map[string]string{
		"type":    "reset-failed",
		"title":   title,
		"message": strings.Join(lines, "\n"),
	})
	s.broadcast(wsMessage{messageType: websocket.TextMessage, data: payload})
}

func (s *Server) addClient(c *client) {
	s.clientsMu.Lock()
	s.clients[c] = struct{}{}
	s.clientsMu.Unlock()
}

func (s *Server) removeClient(c *client) {
	s.clientsMu.Lock()
	delete(s.clients, c)
	s.clientsMu.Unlock()
}

func (s *Server) broadcastOutput() {
	for data := range s.session.Output() {
		s.broadcast(wsMessage{messageType: websocket.BinaryMessage, data: data})
	}
}

func (s *Server) broadcastStatus() {
	for message := range s.session.Status() {
		payload, _ := json.Marshal(map[string]string{
			"type":    "status",
			"message": message,
		})
		s.broadcast(wsMessage{messageType: websocket.TextMessage, data: payload})
	}
}

func (s *Server) broadcast(msg wsMessage) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()

	for c := range s.clients {
		select {
		case c.send <- msg:
		default:
		}
	}
}

func (s *Server) staticHandler() http.Handler {
	webRoot, err := fs.Sub(webFS, "web")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Static assets not available", http.StatusInternalServerError)
		})
	}

	fileServer := http.FileServer(http.FS(webRoot))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "" || r.URL.Path == "/" || r.URL.Path == "/index.html" {
			data, err := fs.ReadFile(webRoot, "index.html")
			if err != nil {
				http.Error(w, "Index not available", http.StatusInternalServerError)
				return
			}
			alias := ""
			if strings.TrimSpace(s.alias) != "" {
				alias = html.EscapeString(s.alias)
			}
			content := strings.ReplaceAll(string(data), "__ALICES_MIRROR_ALIAS__", alias)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(content))
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	if !s.auth.Enabled {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != s.auth.User || pass != s.auth.Password {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf("Basic realm=\"%s\"", "alices mirror"))
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func LocalIPv4s() []string {
	var results []string
	interfaces, err := net.Interfaces()
	if err != nil {
		return results
	}

	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ip := extractIPv4(addr)
			if ip == "" {
				continue
			}
			if strings.HasPrefix(ip, "169.254.") {
				continue
			}
			results = append(results, ip)
		}
	}

	return uniqueStrings(results)
}

func extractIPv4(addr net.Addr) string {
	switch v := addr.(type) {
	case *net.IPNet:
		if v.IP == nil {
			return ""
		}
		ip := v.IP.To4()
		if ip == nil {
			return ""
		}
		return ip.String()
	case *net.IPAddr:
		if v.IP == nil {
			return ""
		}
		ip := v.IP.To4()
		if ip == nil {
			return ""
		}
		return ip.String()
	default:
		return ""
	}
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
