# Chisel GUI

A local, cross-platform tunnel manager. It embeds Chisel as a Go library and
opens its interface in your default browser. A system tray menu lets you reopen
the admin page or quit. Windows runs without a console window, including ordinary
console builds; the release build also uses the Windows GUI subsystem.
No Node.js, frontend build, or WebView runtime is required. Windows and Linux
builds do not require CGO; macOS tray support requires CGO and Xcode command-line tools.

## Run

From the repository root:

```powershell
go run ./cmd/chisel-gui
```

Build the Windows application:

```powershell
./cmd/chisel-gui/build.ps1
# Optional: ./cmd/chisel-gui/build.ps1 -Architecture arm64
./bin/chisel-gui-windows-amd64.exe
```

Double-click the executable to open the GUI. Create a profile, enter the Chisel
server URL, add a tunnel using a preset, adjust the ports, and click **Connect**.
Multiple profiles can run simultaneously. Each profile supports multiple tunnels.
SOCKS proxies and reverse tunnels require the matching Chisel server options.

On macOS or Linux:

```sh
go build -o bin/chisel-gui ./cmd/chisel-gui
./bin/chisel-gui
```

The executable embeds all interface assets and works offline. It connects to
existing Chisel servers; it does not launch or configure a server.

## Lifecycle and settings

- Saved profiles automatically connect when the application starts, using their
  stored credentials. This also works with `-no-browser`. A profile that cannot
  start shows its error in the UI while other profiles continue connecting.
- Closing the browser tab keeps the application and tunnels running. Right-click
  the Chisel tray icon and choose **Open admin page** to return, or **Quit Chisel
  GUI** to disconnect all tunnels and exit. Double-clicking the Windows icon also
  opens the admin page. The browser sidebar has a Quit button too.
- The tray supports Windows, macOS, and Linux/BSD desktops with StatusNotifier
  support (such as KDE; GNOME may need an AppIndicator extension). Headless Linux
  sessions automatically run without a tray. Use `-no-tray` for headless/debug mode;
  on Windows this also preserves the terminal for diagnostics. macOS builds with
  CGO disabled retain the browser UI but have no tray.
- Saved edits apply to the next connection. Disconnect and reconnect to use them.
- Chisel handles reconnection. The default is unlimited retries; `0` disables
  retries. TCP, UDP, SOCKS5, UDP SOCKS, and reverse remotes use Chisel's existing
  syntax. `stdio` remotes require the CLI.
- Advanced settings include authentication, SSH fingerprint pinning, HTTP/SOCKS
  upstream proxies, custom headers, keepalive, retry limits, and TLS/mTLS.
- Use a fingerprint from your server to verify its SSH identity. TLS certificate
  verification is enabled by default. Preset listeners bind to `127.0.0.1`.
- Connection activity shows the last 400 client connection log entries. Child
  tunnel loggers use Chisel's existing logging destinations. Logs can be downloaded.
- Profile settings default to `%APPDATA%/chisel-gui/profiles.json` on Windows, or
  the directory returned by `os.UserConfigDir` on other systems.
- Saving a profile automatically remembers authentication, proxy credentials,
  and HTTP headers across restarts. On Windows those fields are encrypted with
  user-scoped DPAPI; the same Windows account is needed to decrypt them. Existing
  plaintext profiles migrate on their next save. On other platforms complete
  profiles use a file with owner-only permissions. Private keys are referenced
  by file path, not copied into the profile. Credentials discarded by older
  versions must be entered once more and saved.
- Each launch creates a fresh loopback listener and random access token. The URL
  fragment transfers the token to tab session storage and is then removed from
  the address bar. API requests require the token and validate Host and Origin.
  The interface loads no external scripts, fonts, or other assets.

For a fixed URL or an alternate profile file:

```sh
go run ./cmd/chisel-gui -listen 127.0.0.1:8787 -config ./profiles.json
go run ./cmd/chisel-gui -no-browser -no-tray
```

Only loopback IP addresses are accepted. `-no-browser` starts in the tray without
opening the page; **Open admin page** remains available in its menu. Pair it with
`-no-tray` for console diagnostics. Each executable launch is an independent
instance. Avoid running multiple instances against the same profile file.

## Upstream merge boundary

Everything is contained in `cmd/chisel-gui/`, apart from a link in the root README.
The existing CLI, `client/`, `server/`, and `share/` are unchanged. The only new
module dependencies are `energye/systray` and its D-Bus dependency. Windows
credential protection uses the existing `x/sys/windows` dependency.
The adapter uses exported `client.Config`, `NewClient`, `Start`,
`Wait`, the client logger's `SetOutput`, and context cancellation. Remote validation
uses the existing `settings.DecodeRemote` parser.

Connection status observes the client's existing `Connected`, `Disconnected`, and
`Retrying` log messages because the library has no disconnect event subscription.
If upstream changes those messages, adjust `session.Write` in `app.go`.
Tunnel operation remains independent of that display status.

```sh
go test ./cmd/chisel-gui
go vet ./cmd/chisel-gui
```

Tests cover real authenticated and fingerprint-verified traffic through Chisel,
disconnect/reconnect and port release, retry exhaustion, API access controls,
configuration validation, automatic credential persistence across reloads,
Windows credential encryption, and legacy profile migration.
