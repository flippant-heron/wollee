package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"

	internalwol "github.com/flippant-heron/wollee/internal/wol"
)

const defaultConfigFile = "config.yaml"

type Config struct {
	SourcePath string
	Server     ServerConfig
	Client     ClientConfig
}

type ServerConfig struct {
	Port                 int
	SubnetBroadcast      string
	TelegramToken        string
	AllowedTelegramUsers []int64
}

type ClientConfig struct {
	ServerURL         string
	MACAddress        string
	HeartbeatInterval time.Duration
}

type rawConfig struct {
	Server ServerConfig    `mapstructure:"server"`
	Client rawClientConfig `mapstructure:"client"`
}

type rawClientConfig struct {
	ServerURL         string `mapstructure:"serverURL"`
	MACAddress        string `mapstructure:"macAddress"`
	HeartbeatInterval string `mapstructure:"heartbeatInterval"`
}

func DefaultPath() string {
	return defaultConfigFile
}

func Load(path string) (Config, error) {
	if path == "" {
		path = defaultConfigFile
	}

	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	v.SetDefault("server.port", 8080)
	v.SetDefault("client.heartbeatInterval", "30s")

	if err := v.ReadInConfig(); err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var raw rawConfig
	if err := v.Unmarshal(&raw); err != nil {
		return Config{}, fmt.Errorf("unmarshal config: %w", err)
	}

	interval, err := time.ParseDuration(raw.Client.HeartbeatInterval)
	if err != nil && raw.Client.HeartbeatInterval != "" {
		return Config{}, fmt.Errorf("parse client heartbeat interval: %w", err)
	}

	if raw.Client.HeartbeatInterval == "" {
		interval = 30 * time.Second
	}

	return Config{
		SourcePath: v.ConfigFileUsed(),
		Server:     raw.Server,
		Client: ClientConfig{
			ServerURL:         raw.Client.ServerURL,
			MACAddress:        raw.Client.MACAddress,
			HeartbeatInterval: interval,
		},
	}, nil
}

func (c *Config) ValidateServer() error {
	var errs []error

	if c.Server.Port < 1 || c.Server.Port > 65535 {
		errs = append(errs, fmt.Errorf("server.port must be between 1 and 65535"))
	}

	if err := internalwol.ValidateBroadcast(c.Server.SubnetBroadcast); err != nil {
		errs = append(errs, fmt.Errorf("server.subnetBroadcast: %w", err))
	}

	if c.Server.TelegramToken != "" && len(c.Server.AllowedTelegramUsers) == 0 {
		errs = append(errs, errors.New("server.allowedTelegramUsers must contain at least one user when telegramToken is set"))
	}

	for _, userID := range c.Server.AllowedTelegramUsers {
		if userID == 0 {
			errs = append(errs, errors.New("server.allowedTelegramUsers cannot contain 0"))
			break
		}
	}

	return errors.Join(errs...)
}

func (c *Config) ValidateClient() error {
	var errs []error

	if _, err := resolveHTTPURL(c.Client.ServerURL); err != nil {
		errs = append(errs, fmt.Errorf("client.serverURL: %w", err))
	}

	normalizedMAC, err := internalwol.NormalizeMAC(c.Client.MACAddress)
	if err != nil {
		errs = append(errs, fmt.Errorf("client.macAddress: %w", err))
	} else {
		c.Client.MACAddress = normalizedMAC
	}

	if c.Client.HeartbeatInterval <= 0 {
		errs = append(errs, errors.New("client.heartbeatInterval must be greater than 0"))
	}

	return errors.Join(errs...)
}

func RegistryPath(sourcePath string) string {
	if sourcePath == "" {
		return "hosts.yaml"
	}

	return filepath.Join(filepath.Dir(sourcePath), "hosts.yaml")
}

func resolveHTTPURL(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("must not be empty")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("must use http or https")
	}

	if parsed.Host == "" {
		return nil, errors.New("host is required")
	}

	host := parsed.Hostname()
	if host == "" {
		return nil, errors.New("hostname is required")
	}

	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		return nil, errors.New("only IPv4 server hosts are supported")
	}

	return parsed, nil
}
