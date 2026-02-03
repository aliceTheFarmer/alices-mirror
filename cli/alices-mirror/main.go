package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"alices-mirror/internal/app"
)

type flagSpec struct {
	Long         string
	Short        string
	ExpectsValue bool
	IsBool       bool
}

var baseSpecs = []flagSpec{
	{Long: "alias", Short: "a", ExpectsValue: true, IsBool: false},
	{Long: "help", Short: "h", ExpectsValue: false, IsBool: true},
	{Long: "cwd", Short: "cw", ExpectsValue: true, IsBool: false},
	{Long: "daemon", Short: "d", ExpectsValue: false, IsBool: true},
	{Long: "share", Short: "s", ExpectsValue: false, IsBool: true},
	{Long: "share", Short: "sh", ExpectsValue: false, IsBool: true},
	{Long: "bind", Short: "b", ExpectsValue: true, IsBool: false},
	{Long: "allow-ip", Short: "al", ExpectsValue: true, IsBool: false},
	{Long: "allow-ips", Short: "", ExpectsValue: true, IsBool: false},
	{Long: "origin", Short: "o", ExpectsValue: true, IsBool: false},
	{Long: "user-level", Short: "ul", ExpectsValue: true, IsBool: false},
	{Long: "password", Short: "P", ExpectsValue: true, IsBool: false},
	{Long: "port", Short: "p", ExpectsValue: true, IsBool: false},
	{Long: "visible", Short: "vi", ExpectsValue: false, IsBool: true},
	{Long: "user", Short: "u", ExpectsValue: true, IsBool: false},
	{Long: "yolo", Short: "y", ExpectsValue: false, IsBool: true},
}

const defaultBindList = "127.0.0.1,192.168.1.*"
const defaultAllowIPList = "127.0.0.1,192.168.1.*"
const defaultUserLevel = "*-0"

func allSpecs() []flagSpec {
	platform := platformSpecs()
	out := make([]flagSpec, 0, len(baseSpecs)+len(platform))
	out = append(out, baseSpecs...)
	out = append(out, platform...)
	return out
}

func main() {
	canonical, positionals, err := normalizeArgs(os.Args[1:])
	if err != nil {
		printError(err)
		os.Exit(1)
	}
	if len(positionals) > 0 {
		printError(fmt.Errorf("unexpected positional arguments: %s", strings.Join(positionals, " ")))
		os.Exit(1)
	}

	fs := flag.NewFlagSet("alices-mirror", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var (
		alias     string
		help      bool
		cwd       string
		daemon    bool
		share     bool
		bind      string
		origin    string
		allowIPs  string
		userLevel string
		port      int
		visible   bool
		user      string
		password  string
		yolo      bool
		shell     = defaultPlatformShell()
	)

	fs.StringVar(&alias, "alias", "", "")
	fs.BoolVar(&help, "help", false, "")
	fs.StringVar(&cwd, "cwd", "", "")
	fs.BoolVar(&daemon, "daemon", false, "")
	fs.BoolVar(&share, "share", false, "")
	fs.StringVar(&bind, "bind", defaultBindList, "")
	fs.StringVar(&origin, "origin", "", "")
	fs.StringVar(&allowIPs, "allow-ip", defaultAllowIPList, "")
	fs.StringVar(&allowIPs, "allow-ips", defaultAllowIPList, "")
	fs.StringVar(&userLevel, "user-level", defaultUserLevel, "")
	fs.IntVar(&port, "port", 3002, "")
	fs.BoolVar(&visible, "visible", false, "")
	fs.StringVar(&user, "user", "", "")
	fs.StringVar(&password, "password", "", "")
	fs.BoolVar(&yolo, "yolo", false, "")
	registerPlatformFlags(fs, &shell)

	if err := fs.Parse(canonical); err != nil {
		printError(err)
		os.Exit(1)
	}

	if help {
		printHelp()
		return
	}

	if port < 1 || port > 65535 {
		printError(fmt.Errorf("invalid value %q for --port", fmt.Sprintf("%d", port)))
		os.Exit(1)
	}

	bindProvided := flagPresent(canonical, "bind")
	originProvided := flagPresent(canonical, "origin")
	if bindProvided && originProvided {
		printError(errors.New("cannot use --origin with --bind (use --bind only)"))
		os.Exit(1)
	}

	bindFlagName := "--bind"
	bindList := bind
	if originProvided {
		bindFlagName = "--origin"
		bindList = origin
	}

	binds, err := parseHostList(bindList, bindFlagName)
	if err != nil {
		printError(err)
		os.Exit(1)
	}

	allowProvided := flagPresent(canonical, "allow-ip") || flagPresent(canonical, "allow-ips")
	if allowProvided && strings.TrimSpace(allowIPs) == "" {
		printError(fmt.Errorf("invalid value %q for --allow-ip", allowIPs))
		os.Exit(1)
	}
	allowList, err := parseHostList(allowIPs, "--allow-ip")
	if err != nil {
		printError(err)
		os.Exit(1)
	}

	userLevelProvided := flagPresent(canonical, "user-level")
	if userLevelProvided && strings.TrimSpace(userLevel) == "" {
		printError(fmt.Errorf("invalid value %q for --user-level", userLevel))
		os.Exit(1)
	}

	shell, err = normalizePlatformShell(shell)
	if err != nil {
		printError(err)
		os.Exit(1)
	}

	cwdProvided := flagPresent(canonical, "cwd")
	if cwdProvided && strings.TrimSpace(cwd) == "" {
		printError(fmt.Errorf("invalid value %q for --cwd", cwd))
		os.Exit(1)
	}

	workDir, err := resolveWorkDir(cwd, cwdProvided)
	if err != nil {
		printError(err)
		os.Exit(1)
	}

	cfg := app.Config{
		Alias:     alias,
		Port:      port,
		Origins:   binds,
		AllowIPs:  allowList,
		UserLevel: userLevel,
		User:      user,
		Password:  password,
		Yolo:      yolo,
		WorkDir:   workDir,
		Shell:     shell,
		Visible:   visible,
	}

	if share {
		if err := runShare(cfg, canonical, workDir, cwdProvided); err != nil {
			printError(err)
			os.Exit(1)
		}
		return
	}

	if daemon {
		if err := app.Validate(cfg); err != nil {
			printError(err)
			os.Exit(1)
		}
		args := daemonArgs(canonical, workDir, cwdProvided)
		pid, err := startDaemon(args)
		if err != nil {
			printError(fmt.Errorf("failed to start daemon: %v", err))
			os.Exit(1)
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
		return
	}

	if err := app.Run(cfg); err != nil {
		printError(err)
		os.Exit(1)
	}
}

func normalizeArgs(args []string) ([]string, []string, error) {
	longMap := map[string]flagSpec{}
	shortMap := map[string]flagSpec{}
	for _, spec := range allSpecs() {
		longMap[spec.Long] = spec
		if spec.Short != "" {
			shortMap[spec.Short] = spec
		}
	}

	var out []string
	var positionals []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "--") {
			name, value, hasValue := strings.Cut(arg[2:], "=")
			spec, ok := longMap[name]
			if !ok {
				return nil, nil, fmt.Errorf("unknown flag --%s", name)
			}
			if spec.IsBool {
				if hasValue {
					return nil, nil, fmt.Errorf("flag --%s does not take a value", name)
				}
				out = append(out, "--"+spec.Long)
				continue
			}
			if !hasValue {
				return nil, nil, fmt.Errorf("flag --%s requires a value like --%s=<value>", name, name)
			}
			out = append(out, "--"+spec.Long+"="+value)
			continue
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			short := arg[1:]
			spec, ok := shortMap[short]
			if !ok {
				return nil, nil, fmt.Errorf("unknown flag -%s", short)
			}
			if spec.IsBool {
				out = append(out, "--"+spec.Long)
				continue
			}
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("flag -%s requires a value", short)
			}
			value := args[i+1]
			i++
			out = append(out, "--"+spec.Long+"="+value)
			continue
		}

		positionals = append(positionals, arg)
	}

	return out, positionals, nil
}

func printHelp() {
	binary := filepath.Base(os.Args[0])
	fmt.Printf("Usage:\n  %s [options]\n\n", binary)
	fmt.Println("Options:")
	fmt.Println("  -h, --help             Show help and exit.")
	fmt.Println("  -a, --alias=<alias>    Override the browser title host label.")
	fmt.Println("  -cw, --cwd=<path>      Start the shell in the specified working directory.")
	fmt.Println("  -d, --daemon           Run the server in the background.")
	fmt.Println("  -s, --share            Share this terminal session (starts server in background).")
	fmt.Printf("  -b, --bind=<list>      Bind to comma-separated IPs/hosts (default %s).\n", defaultBindList)
	fmt.Printf("  -al, --allow-ip=<list> Allow only matching client IPs (default %s).\n", defaultAllowIPList)
	fmt.Println("                          Alias: --allow-ips.")
	fmt.Println("                          Patterns support '*' wildcard.")
	fmt.Println("  -o, --origin=<list>    Deprecated alias for --bind.")
	fmt.Printf("  -ul, --user-level=<rules>  Per-IP authorization levels (default %s).\n", defaultUserLevel)
	fmt.Println("                          Format: <pattern>-<level>[,...] where level 0=interact, 1=watch-only.")
	fmt.Println("                          Patterns support '*' wildcard. First match wins. Unmatched IPs default to level 0 with a warning.")
	fmt.Println("  -P, --password=<password>  Set Basic Auth password (requires --user).")
	fmt.Println("  -p, --port=<port>      Listen on port <port> (default 3002).")
	fmt.Println("  -vi, --visible         Advertise the server on the LAN for discovery.")
	printPlatformHelp()
	fmt.Println("  -u, --user=<user>      Set Basic Auth user (requires --password).")
	fmt.Println("  -y, --yolo             Disable auth entirely when present.")
}

func resolveWorkDir(cwd string, cwdProvided bool) (string, error) {
	baseDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to determine working directory: %v", err)
	}
	if !cwdProvided {
		return baseDir, nil
	}
	trimmed := strings.TrimSpace(cwd)
	if trimmed == "" {
		return "", errors.New("working directory cannot be empty")
	}
	if filepath.IsAbs(trimmed) {
		return filepath.Clean(trimmed), nil
	}
	return filepath.Clean(filepath.Join(baseDir, trimmed)), nil
}

func daemonArgs(canonical []string, workDir string, cwdProvided bool) []string {
	out := make([]string, 0, len(canonical))
	cwdFlag := "--cwd="
	for _, arg := range canonical {
		if arg == "--daemon" {
			continue
		}
		if strings.HasPrefix(arg, cwdFlag) {
			if cwdProvided {
				out = append(out, cwdFlag+workDir)
			}
			continue
		}
		out = append(out, arg)
	}
	if cwdProvided && !flagPresent(out, "cwd") {
		out = append(out, cwdFlag+workDir)
	}
	return out
}

func flagPresent(args []string, long string) bool {
	prefix := "--" + long
	for _, arg := range args {
		if arg == prefix || strings.HasPrefix(arg, prefix+"=") {
			return true
		}
	}
	return false
}

func parseHostList(raw string, flagName string) ([]string, error) {
	items := strings.Split(raw, ",")
	if len(items) == 0 {
		return nil, fmt.Errorf("invalid value %q for %s", raw, flagName)
	}

	seen := make(map[string]struct{}, len(items))
	values := make([]string, 0, len(items))
	for _, item := range items {
		cleaned := strings.TrimSpace(item)
		if cleaned == "" {
			return nil, fmt.Errorf("invalid value %q for %s", raw, flagName)
		}
		if strings.Contains(cleaned, ":") {
			if net.ParseIP(cleaned) == nil {
				return nil, fmt.Errorf("invalid value %q for %s: hostnames must not include a port", cleaned, flagName)
			}
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		values = append(values, cleaned)
	}

	if len(values) == 0 {
		return nil, fmt.Errorf("invalid value %q for %s", raw, flagName)
	}

	return values, nil
}

func printError(err error) {
	if err == nil {
		return
	}
	message := err.Error()
	if errors.Is(err, flag.ErrHelp) {
		printHelp()
		return
	}
	binary := filepath.Base(os.Args[0])
	fmt.Fprintf(os.Stderr, "Error: %s\n\n", message)
	fmt.Fprintf(os.Stderr, "Usage:\n  %s [options]\n\n", binary)
	fmt.Fprintln(os.Stderr, "Try --help for usage.")
}
