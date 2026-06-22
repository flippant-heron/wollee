package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	kservice "github.com/kardianos/service"
	"github.com/spf13/cobra"

	"github.com/flippant-heron/wollee/internal/config"
	"github.com/flippant-heron/wollee/internal/server"
	appservice "github.com/flippant-heron/wollee/internal/service"
)

type serverRuntime struct {
	service kservice.Service
	program *appservice.Program
	logger  *appservice.Logger
}

func main() {
	if err := newRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "wol-server",
		Short: "Wake-on-LAN registry server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServer(configPath)
		},
	}

	cmd.PersistentFlags().StringVar(&configPath, "config", config.DefaultPath(), "Path to config file")
	cmd.AddCommand(newServerServiceCommand("install", &configPath))
	cmd.AddCommand(newServerServiceCommand("uninstall", &configPath))
	cmd.AddCommand(newServerServiceCommand("start", &configPath))
	cmd.AddCommand(newServerServiceCommand("stop", &configPath))
	cmd.AddCommand(&cobra.Command{
		Use:   "run",
		Short: "Run the server in the foreground",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServer(configPath)
		},
	})

	return cmd
}

func newServerServiceCommand(action string, configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   action,
		Short: action + " the native service",
		RunE: func(cmd *cobra.Command, _ []string) error {
			runtime, err := buildServerService(*configPath)
			if err != nil {
				return err
			}
			return kservice.Control(runtime.service, action)
		},
	}
}

func runServer(configPath string) error {
	runtime, err := buildServerService(configPath)
	if err != nil {
		return err
	}
	return runtime.service.Run()
}

func buildServerService(configPath string) (*serverRuntime, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	if err := cfg.ValidateServer(); err != nil {
		return nil, err
	}

	registry, err := server.OpenRegistry(config.RegistryPath(cfg.SourcePath))
	if err != nil {
		return nil, err
	}
	if err := seedRegistry(registry, cfg.Hosts); err != nil {
		return nil, err
	}

	interactive := kservice.Interactive()
	logger := appservice.NewLogger(interactive)
	runner, err := server.New(cfg.Server, registry, logger)
	if err != nil {
		return nil, err
	}
	program := appservice.NewProgram(runner, logger)

	absConfigPath, err := filepath.Abs(configPath)
	if err != nil {
		return nil, err
	}

	svc, err := kservice.New(program, &kservice.Config{
		Name:        "wol-server",
		DisplayName: "wol-server",
		Description: "Wake-on-LAN registry server",
		Arguments:   []string{"run", "--config", absConfigPath},
	})
	if err != nil {
		return nil, err
	}

	if !interactive {
		serviceLogger, logErr := svc.Logger(nil)
		if logErr == nil {
			logger.SetServiceLogger(serviceLogger)
		}
	}

	return &serverRuntime{service: svc, program: program, logger: logger}, nil
}

func seedRegistry(registry *server.Registry, hosts []config.HostConfig) error {
	for _, host := range hosts {
		record, exists := registry.FindByMAC(host.MAC)
		if exists {
			if strings.TrimSpace(record.Hostname) == "" {
				record.Hostname = host.Hostname
			}
			if err := registry.Upsert(record); err != nil {
				return fmt.Errorf("seed host %s: %w", host.MAC, err)
			}
			continue
		}
		if err := registry.Upsert(server.HostRecord{Hostname: host.Hostname, MAC: host.MAC}); err != nil {
			return fmt.Errorf("seed host %s: %w", host.MAC, err)
		}
	}
	return nil
}
