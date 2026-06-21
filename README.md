# wollee

Minimalist Wake-on-LAN management for a central server and lightweight client agents.

## Components

- `cmd/server`: HTTP API, Telegram bot integration, and embedded web UI.
- `cmd/agent`: heartbeat daemon that registers downstream hosts with the server.

## Configuration

Copy `config.yaml.example` to `config.yaml` and adjust it for your environment.

## Development

- `go test ./...`
- `go build ./cmd/agent`
- `go build ./cmd/server`
- `task assets`
