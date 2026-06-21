package main

import (
	"os"
	"path/filepath"

	kservice "github.com/kardianos/service"
	"github.com/spf13/cobra"

	"github.com/flippant-heron/wollee/internal/config"
	"github.com/flippant-heron/wollee/internal/server"
	appservice "github.com/flippant-heron/wollee/internal/service"
)

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
			svc, _, _, err := buildServerService(*configPath)
			if err != nil {
				return err
			}
			return kservice.Control(svc, action)
		},
	}
}

func runServer(configPath string) error {
	svc, _, _, err := buildServerService(configPath)
	if err != nil {
		return err
	}
	return svc.Run()
}

func buildServerService(configPath string) (kservice.Service, *appservice.Program, *appservice.Logger, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := cfg.ValidateServer(); err != nil {
		return nil, nil, nil, err
	}

	registry, err := server.OpenRegistry(config.RegistryPath(cfg.SourcePath))
	if err != nil {
		return nil, nil, nil, err
	}

	interactive := kservice.Interactive()
	logger := appservice.NewLogger(interactive)
	runner, err := server.New(cfg.Server, registry, logger)
	if err != nil {
		return nil, nil, nil, err
	}
	program := appservice.NewProgram(runner, logger)

	absConfigPath, err := filepath.Abs(configPath)
	if err != nil {
		return nil, nil, nil, err
	}

	svc, err := kservice.New(program, &kservice.Config{
		Name:        "wol-server",
		DisplayName: "wol-server",
		Description: "Wake-on-LAN registry server",
		Arguments:   []string{"run", "--config", absConfigPath},
	})
	if err != nil {
		return nil, nil, nil, err
	}

	if !interactive {
		serviceLogger, logErr := svc.Logger(nil)
		if logErr == nil {
			logger.SetServiceLogger(serviceLogger)
		}
	}

	return svc, program, logger, nil
}
