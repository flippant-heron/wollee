# wollee

Minimalist Wake-on-LAN management with a central server and lightweight agents.

## Rationale

wollee keeps management simple: agents only heartbeat upstream servers, while wake authorization and packet delivery stay centralized. This avoids exposing WoL controls on every host and keeps operations auditable through one API surface.

## Components

- `cmd/server`: registry API, optional Telegram control, and embedded web UI.
- `cmd/agent`: heartbeat daemon that auto-detects local network identity.

## Configuration

Server configuration is file-based (`config.yaml`) and includes static host metadata:

```yaml
server:
  port: 8080
  subnetBroadcast: 192.168.1.255
  defaultHeartbeatInterval: 30s
  telegramToken: ""
  allowedTelegramUsers: []

hosts:
  - hostname: desktop
    mac: 00:11:22:33:44:55
```

`defaultHeartbeatInterval` is returned by `/register` and controls downstream heartbeat cadence.

## Running as applications

### Server

```bash
cp config.yaml.example config.yaml
task assets:sync
go run ./cmd/server run --config ./config.yaml
```

### Agent

```bash
go run ./cmd/agent run --upstream "http://server-a:8080,http://server-b:8080"
```

Agent options:

- `--upstream` (repeatable; comma/space delimited values supported)
- `--register-path` (default `/register`)
- `--request-timeout` (default `10s`)
- `--initial-heartbeat` (default `30s`)

The agent infers hostname, local IPv4, and matching interface MAC dynamically.

## Running as services

Server:

```bash
./wol-server install --config /absolute/path/config.yaml
./wol-server start
./wol-server stop
./wol-server uninstall
```

Agent:

```bash
./wol-agent install --upstream "http://server:8080"
./wol-agent start
./wol-agent stop
./wol-agent uninstall
```

## Telegram commands

If `server.telegramToken` is set and `allowedTelegramUsers` is non-empty:

- `/list`
- `/wake <hostname|mac>`

## Development

- `task lint`
- `task test`
- `task build:local`
- `task assets:sync`
- `task build:release`

## CI/CD

PR and branch CI runs lint/test only.

Release binaries are generated only for tagged pushes (`v*`) using Task-based build steps after asset sync.

## Alternatives considered

- Per-agent local wake control: rejected to avoid distributed access control and duplicated security surfaces.
- Persistent database registry: rejected in favor of simple YAML state for low operational overhead.
