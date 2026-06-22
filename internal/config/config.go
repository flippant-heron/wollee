package config

import (
	"errors"
	"fmt"
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
	Hosts      []HostConfig
}

type ServerConfig struct {
	Port                     int
	SubnetBroadcast          string
	DefaultHeartbeatInterval time.Duration
	TelegramToken            string
	AllowedTelegramUsers     []int64
}

type HostConfig struct {
	Hostname string `mapstructure:"hostname"`
	MAC      string `mapstructure:"mac"`
}

type rawConfig struct {
	Server rawServerConfig `mapstructure:"server"`
	Hosts  []HostConfig    `mapstructure:"hosts"`
}

type rawServerConfig struct {
	Port                     int     `mapstructure:"port"`
	SubnetBroadcast          string  `mapstructure:"subnetBroadcast"`
	DefaultHeartbeatInterval string  `mapstructure:"defaultHeartbeatInterval"`
	TelegramToken            string  `mapstructure:"telegramToken"`
	AllowedTelegramUsers     []int64 `mapstructure:"allowedTelegramUsers"`
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
	v.SetDefault("server.defaultHeartbeatInterval", "30s")

	if err := v.ReadInConfig(); err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var raw rawConfig
	if err := v.Unmarshal(&raw); err != nil {
		return Config{}, fmt.Errorf("unmarshal config: %w", err)
	}

	interval, err := time.ParseDuration(raw.Server.DefaultHeartbeatInterval)
	if err != nil {
		return Config{}, fmt.Errorf("parse server.defaultHeartbeatInterval: %w", err)
	}

	return Config{
		SourcePath: v.ConfigFileUsed(),
		Server: ServerConfig{
			Port:                     raw.Server.Port,
			SubnetBroadcast:          raw.Server.SubnetBroadcast,
			DefaultHeartbeatInterval: interval,
			TelegramToken:            raw.Server.TelegramToken,
			AllowedTelegramUsers:     raw.Server.AllowedTelegramUsers,
		},
		Hosts: raw.Hosts,
	}, nil
}

func (c *Config) ValidateServer() error {
	var errs []error

	if c.Server.Port < 1 || c.Server.Port > 65535 {
		errs = append(errs, errors.New("server.port must be between 1 and 65535"))
	}

	if err := internalwol.ValidateBroadcast(c.Server.SubnetBroadcast); err != nil {
		errs = append(errs, fmt.Errorf("server.subnetBroadcast: %w", err))
	}

	if c.Server.DefaultHeartbeatInterval <= 0 {
		errs = append(errs, errors.New("server.defaultHeartbeatInterval must be greater than 0"))
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

	for i, host := range c.Hosts {
		hostPath := fmt.Sprintf("hosts[%d]", i)
		if strings.TrimSpace(host.Hostname) == "" {
			errs = append(errs, fmt.Errorf("%s.hostname must not be empty", hostPath))
		}
		normalized, err := internalwol.NormalizeMAC(host.MAC)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s.mac: %w", hostPath, err))
			continue
		}
		c.Hosts[i].MAC = normalized
	}

	return errors.Join(errs...)
}

func RegistryPath(sourcePath string) string {
	if sourcePath == "" {
		return "hosts.yaml"
	}

	return filepath.Join(filepath.Dir(sourcePath), "hosts.yaml")
}
