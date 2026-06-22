# wollee

Minimalist Wake-on-LAN management service, with a central server and lightweight downstream agents, written in go.

## Rationale

wollee keeps power management simple: download the agent onto a downstream device and set it up as a service that sends heartbeats to an upstream server. The upstream server listens for heartbeats and keeps a centralized registry of connected clients. When a downstream client is unresponsive, the server can send a WoL packet to the client to wake it up.

## Components

- `cmd/server`: simple registry API and embedded web UI written with vanilla go http. includes optional Telegram bot integration. the yaml config file serves as a simple flat file database.
- `cmd/agent`: heartbeat daemon that auto-detects local network identity of device and sends it to the central server.

## Configuration

Server configuration is file-based (`config.yaml`) and includes static host metadata:

```yaml
server:
  port: 8080
  subnetBroadcast: 192.168.1.255
  defaultHeartbeatInterval: 30s
  activeTimeout: 5m
  telegramToken: ""
  allowedTelegramUsers: []

hosts:
  - hostname: desktop
    mac: 00:11:22:33:44:55

```

`defaultHeartbeatInterval` is returned by `/register` and controls downstream heartbeat cadence.

## Design

Simple client/server setup where the assumption is simply that if a connection goes stale (active timeout expires since last heartbeat), the downstream service is considered to be offline.

There is no complex state checking mechanism. The server just listens for heartbeats and the downstream agents send them periodically over standard http/s.

You are responsible for the configuration of the other machines, managing the network, controlling how devices are accessed and shutdown, and for setting up the WoL agent services on target devices yourself.

There are many frameworks which try to work across proxies, traverse subnets, manage system power, integrate with frameworks (e.g. home assistant), or use fanciful protocol hacks to sense and control network devices. This (very intentionally) does none of those things.

## Running as applications

### Server

```bash
cp config.yaml.example config.yaml
task assets:dl
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

Uses [taskfiles](https://taskfile.dev) for development.

- `task lint`
- `task test`
- `task build:local`
- `task assets:dl`
- `task build:release`
