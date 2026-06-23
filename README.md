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
hosts:
  - hostname: desktop
    mac: 00:11:22:33:44:55
server:
  port: 8080
  subnetBroadcast: 192.168.1.255
  defaultHeartbeatInterval: 30s
  activeTimeout: 5m
  telegramToken: ""                    # Optional: Telegram bot token from @BotFather
  allowedTelegramUsers: []             # Optional: Telegram user IDs authorized to use bot commands
```

`defaultHeartbeatInterval` is returned by `/register` and controls downstream heartbeat cadence.

Config is reloaded by default every 300 seconds (5 minutes) and will restart the telegram agent if changes to telegram config are detected. If changes are made you can also manually refresh the config via the `/config/reload` api endpoint or from the UI.

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

## Telegram Integration

### Setting Up a Telegram Bot

1. **Create a bot with BotFather**:
   - Message [@BotFather](https://t.me/botfather) on Telegram
   - Send `/newbot` and follow the prompts
   - BotFather will provide you with a token (e.g., `123456789:ABCdefGHIjklmnoPQRstuvWXYZ1234567`)
   - Store this token securely in your `config.yaml` as `server.telegramToken`

2. **Discover your Telegram user ID**:
   - Message your bot and send `/whoami`
   - The bot will reply with your user ID (e.g., `Your Telegram user ID is: 123456789`)
   - Add this ID to `server.allowedTelegramUsers` in your `config.yaml`

3. **Configuration example**:

   ```yaml
   server:
     telegramToken: "123456789:ABCdefGHIjklmnoPQRstuvWXYZ1234567"
     allowedTelegramUsers:
       - 123456789    # Your Telegram user ID
       - 987654321    # Another authorized user's ID (optional)
   ```

### Available Commands

- `/whoami` — Returns your Telegram user ID (works for anyone, even unauthorized users)
- `/list` — Lists all registered hosts and their status (online/offline) [authorized users only]
- `/wake <hostname|mac>` — Sends a WoL packet to wake the specified host [authorized users only]
  - Example: `/wake desktop` or `/wake 00:11:22:33:44:55`

### Security Notes

- **`allowedTelegramUsers`** is a whitelist that controls who can use the `/list` and `/wake` commands. Only Telegram user IDs in this list can execute these commands.
- The `/whoami` command is always available to help users discover their ID
- Keep your `telegramToken` private — anyone with this token can control your bot.
- The token should be stored securely (e.g., environment variables, secrets management, or restricted file permissions).

## Development

Uses [taskfiles](https://taskfile.dev) for development.

- `task lint`
- `task test`
- `task build:local`
- `task assets:dl`
- `task build:release`
