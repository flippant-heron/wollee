package config

import (
	"errors"
	"fmt"
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
	Port          int
	Network       string
	Heartbeat     time.Duration
	Timeout       time.Duration
	ConfigRefresh time.Duration
	Token         string
	Users         []int64
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
	Port          int     `mapstructure:"port"`
	Network       string  `mapstructure:"network"`
	Heartbeat     string  `mapstructure:"heartbeat"`
	Timeout       string  `mapstructure:"timeout"`
	ConfigRefresh string  `mapstructure:"configRefresh"`
	Token         string  `mapstructure:"token"`
	Users         []int64 `mapstructure:"users"`
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
	v.SetDefault("server.heartbeat", "30s")
	v.SetDefault("server.timeout", "5m")
	v.SetDefault("server.configRefresh", "5m")

	if err := v.ReadInConfig(); err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var raw rawConfig
	if err := v.Unmarshal(&raw); err != nil {
		return Config{}, fmt.Errorf("unmarshal config: %w", err)
	}

	interval, err := time.ParseDuration(raw.Server.Heartbeat)
	if err != nil {
		return Config{}, fmt.Errorf("parse server.heartbeat: %w", err)
	}

	timeout, err := time.ParseDuration(raw.Server.Timeout)
	if err != nil {
		return Config{}, fmt.Errorf("parse server.timeout: %w", err)
	}

	cfgRefresh, err := time.ParseDuration(raw.Server.ConfigRefresh)
	if err != nil {
		return Config{}, fmt.Errorf("parse server.configRefresh: %w", err)
	}

	return Config{
		SourcePath: v.ConfigFileUsed(),
		Server: ServerConfig{
			Port:          raw.Server.Port,
			Network:       raw.Server.Network,
			Heartbeat:     interval,
			Timeout:       timeout,
			ConfigRefresh: cfgRefresh,
			Token:         raw.Server.Token,
			Users:         raw.Server.Users,
		},
		Hosts: raw.Hosts,
	}, nil
}

func (c *Config) ValidateServer() error {
	var errs []error

	if c.Server.Port < 1 || c.Server.Port > 65535 {
		errs = append(errs, errors.New("server.port must be between 1 and 65535"))
	}

	if err := internalwol.ValidateBroadcast(c.Server.Network); err != nil {
		errs = append(errs, fmt.Errorf("server.network: %w", err))
	}

	if c.Server.Heartbeat <= 0 {
		errs = append(errs, errors.New("server.heartbeat must be greater than 0"))
	}

	if c.Server.Timeout <= 0 {
		errs = append(errs, errors.New("server.timeout must be greater than 0"))
	}

	if c.Server.ConfigRefresh <= 0 {
		errs = append(errs, errors.New("server.configRefresh must be greater than 0"))
	}

	if c.Server.Token != "" && len(c.Server.Users) == 0 {
		errs = append(errs, errors.New("server.users must contain at least one user when token is set"))
	}

	for _, userID := range c.Server.Users {
		if userID == 0 {
			errs = append(errs, errors.New("server.users cannot contain 0"))
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
