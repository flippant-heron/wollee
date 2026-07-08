package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
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
	Whoami        bool
	PasswordHash  string
	JWTSecret     string
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
	Whoami        bool    `mapstructure:"whoami"`
	PasswordHash  string  `mapstructure:"passwordHash"`
	JWTSecret     string  `mapstructure:"jwtSecret"`
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
	v.SetDefault("server.whoami", false)

	// Bind environment variables for sensitive config
	if err := v.BindEnv("server.jwtSecret", "WOLLEE_JWT_SECRET"); err != nil {
		return Config{}, fmt.Errorf("bind env server.jwtSecret: %w", err)
	}
	if err := v.BindEnv("server.passwordHash", "WOLLEE_PASSWORD_HASH"); err != nil {
		return Config{}, fmt.Errorf("bind env server.passwordHash: %w", err)
	}

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
			Whoami:        raw.Server.Whoami,
			PasswordHash:  raw.Server.PasswordHash,
			JWTSecret:     getOrGenerateJWTSecret(raw.Server.JWTSecret),
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

// getOrGenerateJWTSecret returns the provided secret or generates a new one if empty
func getOrGenerateJWTSecret(secret string) string {
	if secret != "" {
		return secret
	}

	// Try to get from environment variable
	if envSecret := os.Getenv("WOLLEE_JWT_SECRET"); envSecret != "" {
		return envSecret
	}

	// Generate a new random 32-byte secret
	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		// Fallback to a static secret if generation fails (shouldn't happen)
		return "wollee-generated-secret-fallback"
	}

	return base64.StdEncoding.EncodeToString(randomBytes)
}
