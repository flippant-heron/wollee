package main

import (
	"os"
	"path/filepath"

	kservice "github.com/kardianos/service"
	"github.com/spf13/cobra"

	"github.com/flippant-heron/wollee/internal/agent"
	"github.com/flippant-heron/wollee/internal/config"
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
		Use:   "wol-agent",
		Short: "Wake-on-LAN heartbeat agent",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAgent(configPath)
		},
	}

	cmd.PersistentFlags().StringVar(&configPath, "config", config.DefaultPath(), "Path to config file")
	cmd.AddCommand(newAgentServiceCommand("install", &configPath))
	cmd.AddCommand(newAgentServiceCommand("uninstall", &configPath))
	cmd.AddCommand(newAgentServiceCommand("start", &configPath))
	cmd.AddCommand(newAgentServiceCommand("stop", &configPath))
	cmd.AddCommand(&cobra.Command{
		Use:   "run",
		Short: "Run the agent in the foreground",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAgent(configPath)
		},
	})

	return cmd
}

func newAgentServiceCommand(action string, configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   action,
		Short: action + " the native service",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, _, _, err := buildAgentService(*configPath)
			if err != nil {
				return err
			}
			return kservice.Control(svc, action)
		},
	}
}

func runAgent(configPath string) error {
	svc, _, _, err := buildAgentService(configPath)
	if err != nil {
		return err
	}
	return svc.Run()
}

func buildAgentService(configPath string) (kservice.Service, *appservice.Program, *appservice.Logger, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := cfg.ValidateClient(); err != nil {
		return nil, nil, nil, err
	}

	interactive := kservice.Interactive()
	logger := appservice.NewLogger(interactive)
	runner := agent.New(cfg.Client, logger)
	program := appservice.NewProgram(runner, logger)

	absConfigPath, err := filepath.Abs(configPath)
	if err != nil {
		return nil, nil, nil, err
	}

	svc, err := kservice.New(program, &kservice.Config{
		Name:        "wol-agent",
		DisplayName: "wol-agent",
		Description: "Wake-on-LAN heartbeat agent",
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
