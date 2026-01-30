package mobile

import (
    "context"
    "errors"
    "fmt"
    "net"
    "os"
    "runtime"
    "strings"
    "sync"

    "alices-mirror/internal/app"
    "alices-mirror/internal/discovery"
    "alices-mirror/internal/server"
    "alices-mirror/internal/terminal"
)

// Listener receives status and log updates from the server.
type Listener interface {
    OnLog(line string)
    OnStatus(message string)
    OnError(message string)
}

// Server exposes Alice's Mirror for mobile bindings.
type Server struct {
    mu        sync.Mutex
    running   bool
    listener  Listener
    session   *terminal.Session
    server    *server.Server
    discovery *discovery.Service
    cancel    context.CancelFunc
}

// NewServer creates a new server wrapper.
func NewServer(listener Listener) *Server {
    return &Server{listener: listener}
}

// IsRunning reports whether the server is running.
func (s *Server) IsRunning() bool {
    s.mu.Lock()
    running := s.running
    s.mu.Unlock()
    return running
}

// Start launches the server with the provided configuration.
func (s *Server) Start(
    alias string,
    workDir string,
    originCsv string,
    port int,
    user string,
    password string,
    yolo bool,
    shell string,
    visible bool,
) error {
    s.mu.Lock()
    if s.running {
        s.mu.Unlock()
        return errors.New("server is already running")
    }
    s.mu.Unlock()

    origins, err := parseOriginList(originCsv)
    if err != nil {
        return err
    }

    cfg := app.Config{
        Alias:    alias,
        Port:     port,
        Origins:  origins,
        User:     user,
        Password: password,
        Yolo:     yolo,
        WorkDir:  workDir,
        Shell:    shell,
        Visible:  visible,
    }

    if err := app.Validate(cfg); err != nil {
        return err
    }

    session, err := terminal.NewSession(terminal.Config{
        WorkDir:    cfg.WorkDir,
        BufferSize: 256 * 1024,
        Shell:      cfg.Shell,
    })
    if err != nil {
        return err
    }

    addrs := make([]string, 0, len(cfg.Origins))
    for _, origin := range cfg.Origins {
        addrs = append(addrs, net.JoinHostPort(origin, fmt.Sprintf("%d", cfg.Port)))
    }
    auth := app.BuildAuthConfig(cfg)
    srv, err := server.New(server.Config{
        Addrs:   addrs,
        Session: session,
        Auth:    auth,
        Alias:   strings.TrimSpace(cfg.Alias),
    })
    if err != nil {
        session.Close()
        return err
    }

    ctx, cancel := context.WithCancel(context.Background())

    s.mu.Lock()
    s.running = true
    s.session = session
    s.server = srv
    s.cancel = cancel
    s.mu.Unlock()

    go s.forwardStatus(session)

    if cfg.Visible {
        hostname, _ := os.Hostname()
        svc, err := discovery.Start(ctx, discovery.Info{
            Alias:        strings.TrimSpace(cfg.Alias),
            Hosts:        filterLanHosts(buildDisplayHosts(cfg.Origins)),
            Port:         cfg.Port,
            AuthRequired: auth.Enabled,
            Yolo:         cfg.Yolo,
            Version:      readVersion(),
            Shell:        cfg.Shell,
            OS:           runtime.GOOS,
            WorkDir:      cfg.WorkDir,
            Hostname:     hostname,
        })
        if err != nil {
            cancel()
            session.Close()
            s.setRunning(false)
            return err
        }
        s.mu.Lock()
        s.discovery = svc
        s.mu.Unlock()
    }

    for _, line := range app.StartupLines(app.StartupInfo{
        WorkDir: cfg.WorkDir,
        Port:    cfg.Port,
        Origins: cfg.Origins,
        Auth:    auth,
        PID:     0,
        Daemon:  false,
    }) {
        s.emitLog(line)
    }

    go func() {
        err := srv.Start(ctx)
        if err != nil && ctx.Err() == nil {
            s.emitError(err.Error())
        }
        s.setRunning(false)
    }()

    return nil
}

// Stop stops the running server.
func (s *Server) Stop() error {
    s.mu.Lock()
    if !s.running {
        s.mu.Unlock()
        return errors.New("server is not running")
    }
    cancel := s.cancel
    session := s.session
    s.cancel = nil
    s.session = nil
    s.server = nil
    s.discovery = nil
    s.running = false
    s.mu.Unlock()

    if cancel != nil {
        cancel()
    }
    if session != nil {
        session.Close()
    }
    return nil
}

func (s *Server) forwardStatus(session *terminal.Session) {
    for message := range session.Status() {
        s.emitStatus(message)
    }
}

func (s *Server) emitLog(line string) {
    if s.listener == nil || strings.TrimSpace(line) == "" {
        return
    }
    s.listener.OnLog(line)
}

func (s *Server) emitStatus(message string) {
    if s.listener == nil || strings.TrimSpace(message) == "" {
        return
    }
    s.listener.OnStatus(message)
}

func (s *Server) emitError(message string) {
    if s.listener == nil || strings.TrimSpace(message) == "" {
        return
    }
    s.listener.OnError(message)
}

func (s *Server) setRunning(value bool) {
    s.mu.Lock()
    s.running = value
    s.mu.Unlock()
}

func buildDisplayHosts(origins []string) []string {
    var hosts []string
    for _, origin := range origins {
        if origin == "0.0.0.0" {
            hosts = append(hosts, server.LocalIPv4s()...)
            continue
        }
        hosts = append(hosts, origin)
    }
    return uniqueStrings(hosts)
}

func filterLanHosts(hosts []string) []string {
    var out []string
    for _, host := range hosts {
        trimmed := strings.TrimSpace(host)
        if trimmed == "" {
            continue
        }
        if strings.EqualFold(trimmed, "localhost") {
            continue
        }
        if ip := net.ParseIP(trimmed); ip != nil && ip.IsLoopback() {
            continue
        }
        out = append(out, trimmed)
    }
    return uniqueStrings(out)
}

func uniqueStrings(values []string) []string {
    seen := make(map[string]struct{}, len(values))
    var out []string
    for _, value := range values {
        if value == "" {
            continue
        }
        if _, ok := seen[value]; ok {
            continue
        }
        seen[value] = struct{}{}
        out = append(out, value)
    }
    return out
}

func readVersion() string {
    data, err := os.ReadFile("VERSION")
    if err != nil {
        return "unknown"
    }
    value := strings.TrimSpace(string(data))
    if value == "" {
        return "unknown"
    }
    return value
}

func parseOriginList(raw string) ([]string, error) {
    items := strings.Split(raw, ",")
    if len(items) == 0 {
        return nil, errors.New("invalid origin list")
    }

    seen := make(map[string]struct{}, len(items))
    origins := make([]string, 0, len(items))
    for _, item := range items {
        cleaned := strings.TrimSpace(item)
        if cleaned == "" {
            return nil, errors.New("origin list contains an empty entry")
        }
        if strings.Contains(cleaned, ":") {
            if net.ParseIP(cleaned) == nil {
                return nil, errors.New("invalid origin: hostnames must not include a port")
            }
        }
        if _, ok := seen[cleaned]; ok {
            continue
        }
        seen[cleaned] = struct{}{}
        origins = append(origins, cleaned)
    }

    if len(origins) == 0 {
        return nil, errors.New("origin list is empty")
    }

    return origins, nil
}
