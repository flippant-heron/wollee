package appservice

import (
	"encoding/json"
	"log/slog"
	"os"
	"sync"

	sysservice "github.com/kardianos/service"
)

type Logger struct {
	mu          sync.RWMutex
	interactive bool
	stdout      *slog.Logger
	service     sysservice.Logger
}

func NewLogger(interactive bool) *Logger {
	return &Logger{
		interactive: interactive,
		stdout: slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})),
	}
}

func (l *Logger) SetServiceLogger(logger sysservice.Logger) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.service = logger
}

func (l *Logger) Info(msg string, attrs ...any) {
	l.log("INFO", msg, attrs...)
}

func (l *Logger) Warning(msg string, attrs ...any) {
	l.log("WARN", msg, attrs...)
}

func (l *Logger) Error(msg string, err error, attrs ...any) {
	if err != nil {
		attrs = append(attrs, "error", err.Error())
	}
	l.log("ERROR", msg, attrs...)
}

func (l *Logger) log(level string, msg string, attrs ...any) {
	l.mu.RLock()
	serviceLogger := l.service
	interactive := l.interactive
	stdout := l.stdout
	l.mu.RUnlock()

	if interactive || serviceLogger == nil {
		switch level {
		case "ERROR":
			stdout.Error(msg, attrs...)
		case "WARN":
			stdout.Warn(msg, attrs...)
		default:
			stdout.Info(msg, attrs...)
		}
		return
	}

	entry := map[string]any{
		"level": level,
		"msg":   msg,
	}
	for idx := 0; idx < len(attrs); idx += 2 {
		key, ok := attrs[idx].(string)
		if !ok {
			key = "field"
		}
		if idx+1 >= len(attrs) {
			entry[key] = ""
			continue
		}
		entry[key] = attrs[idx+1]
	}

	payload, err := json.Marshal(entry)
	if err != nil {
		payload = []byte(msg)
	}

	switch level {
	case "ERROR":
		_ = serviceLogger.Error(string(payload))
	case "WARN":
		_ = serviceLogger.Warning(string(payload))
	default:
		_ = serviceLogger.Info(string(payload))
	}
}
