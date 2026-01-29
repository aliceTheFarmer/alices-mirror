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
	{Long: "help", Short: "h", ExpectsValue: false, IsBool: true},
	{Long: "origin", Short: "o", ExpectsValue: true, IsBool: false},
	{Long: "password", Short: "P", ExpectsValue: true, IsBool: false},
	{Long: "port", Short: "p", ExpectsValue: true, IsBool: false},
	{Long: "user", Short: "u", ExpectsValue: true, IsBool: false},
	{Long: "yolo", Short: "y", ExpectsValue: false, IsBool: true},
}

const defaultOrigins = "127.0.0.1,192.168.1.121"

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
		help     bool
		origin   string
		port     int
		user     string
		password string
		yolo     bool
		shell    = defaultPlatformShell()
	)

	fs.BoolVar(&help, "help", false, "")
	fs.StringVar(&origin, "origin", defaultOrigins, "")
	fs.IntVar(&port, "port", 3002, "")
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

	origins, err := parseOriginList(origin)
	if err != nil {
		printError(err)
		os.Exit(1)
	}

	shell, err = normalizePlatformShell(shell)
	if err != nil {
		printError(err)
		os.Exit(1)
	}

	workDir, err := os.Getwd()
	if err != nil {
		printError(fmt.Errorf("failed to determine working directory: %v", err))
		os.Exit(1)
	}

	if err := app.Run(app.Config{
		Port:     port,
		Origins:  origins,
		User:     user,
		Password: password,
		Yolo:     yolo,
		WorkDir:  workDir,
		Shell:    shell,
	}); err != nil {
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
			if len(arg) != 2 {
				return nil, nil, fmt.Errorf("unknown flag %s", arg)
			}
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
	fmt.Printf("  -o, --origin=<list>    Bind to comma-separated IPs/hosts (default %s).\n", defaultOrigins)
	fmt.Println("  -P, --password=<password>  Set Basic Auth password (requires --user).")
	fmt.Println("  -p, --port=<port>      Listen on port <port> (default 3002).")
	printPlatformHelp()
	fmt.Println("  -u, --user=<user>      Set Basic Auth user (requires --password).")
	fmt.Println("  -y, --yolo             Disable auth entirely when present.")
}

func parseOriginList(raw string) ([]string, error) {
	items := strings.Split(raw, ",")
	if len(items) == 0 {
		return nil, fmt.Errorf("invalid value %q for --origin", raw)
	}

	seen := make(map[string]struct{}, len(items))
	origins := make([]string, 0, len(items))
	for _, item := range items {
		cleaned := strings.TrimSpace(item)
		if cleaned == "" {
			return nil, fmt.Errorf("invalid value %q for --origin", raw)
		}
		if strings.Contains(cleaned, ":") {
			if net.ParseIP(cleaned) == nil {
				return nil, fmt.Errorf("invalid origin %q: hostnames must not include a port", cleaned)
			}
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		origins = append(origins, cleaned)
	}

	if len(origins) == 0 {
		return nil, fmt.Errorf("invalid value %q for --origin", raw)
	}

	return origins, nil
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
