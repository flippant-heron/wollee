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
	Logo       LogoConfig
}

// LogoConfig configures an optional nav-bar logo. Base64 takes precedence
// over Path; if neither resolves to a usable image, the app falls back to
// showing the "wollee" text brand. Path is a filename relative to the
// embedded web/static assets (served at /static/), not a filesystem path.
// FaviconMode controls how the logo is cropped down to favicon size; see
// server.generateFavicon for the supported values. An empty value uses the
// default ("resize-left").
type LogoConfig struct {
	Base64      string
	Path        string
	FaviconMode string `mapstructure:"faviconMode"`
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
	Logo   LogoConfig      `mapstructure:"logo"`
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
	v.SetDefault("server.network", "192.168.1.0/24")
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

	configExists := true
	if err := v.ReadInConfig(); err != nil {
		// Only allow missing config files; other errors (parse errors, etc.) should fail
		if !os.IsNotExist(err) && !isConfigNotFoundError(err) {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
		// File doesn't exist - use defaults and write them to disk
		configExists = false
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

	cfg := Config{
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
		Logo:  raw.Logo,
	}

	// If config file didn't exist, write defaults to disk
	if !configExists {
		if err := writeDefaultConfig(path, cfg); err != nil {
			return Config{}, fmt.Errorf("write default config: %w", err)
		}
	}

	return cfg, nil
}

// writeDefaultConfig writes a YAML config file with defaults to the given path
func writeDefaultConfig(path string, cfg Config) error {
	content := `
hosts: []
  # Add your devices here, e.g.:
  # - hostname: desktop
  #   mac: 00:11:22:33:44:55
  # - hostname: laptop
  #   mac: aa:bb:cc:dd:ee:ff

server:
  port: ` + fmt.Sprintf("%d", cfg.Server.Port) + `
  # Broadcast address in CIDR notation for sending WOL magic packets.
  # This is network-specific and must match your local network.
  # Examples:
  #   192.168.1.0/24   (common for 192.168.1.x networks)
  #   192.168.0.0/24   (common for 192.168.0.x networks)
  #   10.0.0.0/8       (Class A private network)
  # You can reconfigure this anytime in the web UI (/settings).
  network: ` + cfg.Server.Network + `
  heartbeat: ` + cfg.Server.Heartbeat.String() + `
  timeout: ` + cfg.Server.Timeout.String() + `
  configRefresh: ` + cfg.Server.ConfigRefresh.String() + `
  # Optional: Telegram bot token from @BotFather (leave empty to disable)
  token: ""
  # Optional: List of Telegram user IDs authorized to use bot commands
  # Get your user ID by messaging your bot and sending /whoami
  users: []
    # - 123456789     # Your Telegram user ID
    # - 987654321     # Another authorized user (optional)
  # Optional: Allow unauthenticated /whoami command (default: false)
  whoami: ` + fmt.Sprintf("%v", cfg.Server.Whoami) + `

logo:
  # Preferred: filename of an image already placed in web/static/ (rebuild
  # after adding it). Served at /static/<path>. Ignored if base64 is set.
  path: logo.png
  # Alternative: raw base64-encoded image bytes, embedded directly here.
  # Useful when you do not want to rebuild the binary. Takes precedence
  # over path if both are set.
  # base64: iVBORw0KGgo...
`

	return os.WriteFile(path, []byte(content), 0o600)
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

// isConfigNotFoundError checks if the error is a viper ConfigFileNotFoundError
func isConfigNotFoundError(err error) bool {
	_, ok := err.(viper.ConfigFileNotFoundError)
	return ok
}
