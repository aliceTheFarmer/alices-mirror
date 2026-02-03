# Changelog

## v2.0.2 - 2026-02-03
- Added `--user-level` to set per-IP access levels (0=interact, 1=watch-only).
- Watch-only clients now get a read-only UI and cannot send input/reset/resize.
- Security: use `--allow-ip` to restrict who can connect, and `--user-level` to set watch-only clients.
- Added official mobile (Android arm64 / Termux) release binary: `alices-mirror_mobile`.
- Windows: switched PTY backend to ConPTY.

## v2.0.1 - 2026-02-02
- Added `-s, --share` to start the server in the background and attach this terminal to the shared shell.
- Kept `-sh` as a backward-compatible alias for `--share`.
- Windows: `--shell` shorthand is now `-S` (was `-s`).

## v2.0.0 - 2026-01-30
- Added daemon mode (`--daemon`) to run the server in the background with a PID readout.
- Added `--cwd` and `--alias` to control the working directory and display name.
- Added LAN discovery via mDNS and UDP broadcast (`--visible`) with rich metadata.
- Added shell reset support to terminate the current process tree and respawn cleanly.
- Added dynamic tab titles with cwd + active command for Bash, PowerShell, and cmd.
- Improved the web UI with live status, reset control, markdown shortcuts, clipboard handling, and app icons.
- Added a mobile package to embed the server in mobile bindings.

## v0.1.0 - 2026-01-29
- Initial public release.
