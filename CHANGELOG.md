# Changelog

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
