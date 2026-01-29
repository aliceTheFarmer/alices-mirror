# AGENTS

## Final (Phase 8)

- Project name: alices mirror
- Short description: Serve a shared, persistent Bash terminal over HTTP on the LAN with a mobile-friendly web UI.
- Active toggles:
  - USE_DEFAULT_LOG_SYSTEM: false
  - IMPLEMENT_CONFIG_LOADING: false
  - CREATE_MAKEFILE: false
  - USE_MENU_UI: false
- Binaries list: alices-mirror_linux, alices-mirror_windows.exe
- Package vs CLI mapping:
  - internal/terminal: PTY session lifecycle, output buffer, input handling, resize
  - internal/server: HTTP/WebSocket server, auth middleware, static assets, LAN IP discovery
  - internal/app: orchestration and startup output
  - cli/alices-mirror: flag parsing and app bootstrap
- Flag schema:
  - help (bool, default false) as -h, --help
  - origin (string, default 127.0.0.1,192.168.1.121) as -o, --origin=<ip1,ip2,...>
  - password (string, default empty) as -P, --password=<password>
  - port (int, default 3002) as -p, --port=<port>
  - shell (string, default powershell) as -s, --shell=<shell> (Windows only; powershell|cmd)
  - user (string, default empty) as -u, --user=<user>
  - yolo (bool, default false) as -y, --yolo (overrides user/password)
- Config schema summary: not applicable (config loading disabled)
- Logging summary: not applicable (default logging disabled)
- Menu UI summary: not applicable (menu UI disabled)
- Makefile summary: not applicable (Makefile disabled)
- Resources usage: Resources/ not present
- Binary behavior summary:
  - alices-mirror_linux: starts an HTTP server on the origins list (default 127.0.0.1,192.168.1.121) and port 3002, serves a shared persistent Bash PTY at / with WebSocket at /ws, mobile-friendly key bar, clipboard-aware paste/copy, and optional Basic Auth enabled only when both --user and --password are provided (and --yolo is not set). The shell respawns if it exits; exit/logout/Ctrl+D prompt for confirmation in the UI.
  - alices-mirror_windows.exe: starts the same HTTP server and UI, serving a shared persistent Windows shell PTY (powershell or cmd via --shell, default powershell) with the same auth behavior and respawn semantics.
