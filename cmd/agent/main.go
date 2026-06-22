package main

import (
	"fmt"
	"os"
	"time"

	kservice "github.com/kardianos/service"
	"github.com/spf13/cobra"

	"github.com/flippant-heron/wollee/internal/agent"
	appservice "github.com/flippant-heron/wollee/internal/service"
)

type cliOptions struct {
	upstream         []string
	registerPath     string
	requestTimeout   time.Duration
	initialHeartbeat time.Duration
}

type serviceRuntime struct {
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
	opts := &cliOptions{}

	cmd := &cobra.Command{
		Use:   "wol-agent",
		Short: "Wake-on-LAN heartbeat agent",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAgent(*opts)
		},
	}

	cmd.PersistentFlags().StringSliceVarP(&opts.upstream, "upstream", "u", nil, "Upstream server URLs (comma or space separated)")
	cmd.PersistentFlags().StringVar(&opts.registerPath, "register-path", "/register", "Register API path")
	cmd.PersistentFlags().DurationVar(&opts.requestTimeout, "request-timeout", 10*time.Second, "HTTP request timeout")
	cmd.PersistentFlags().DurationVar(&opts.initialHeartbeat, "initial-heartbeat", 30*time.Second, "Initial heartbeat interval until server response")

	cmd.AddCommand(newAgentServiceCommand("install", opts))
	cmd.AddCommand(newAgentServiceCommand("uninstall", opts))
	cmd.AddCommand(newAgentServiceCommand("start", opts))
	cmd.AddCommand(newAgentServiceCommand("stop", opts))
	cmd.AddCommand(&cobra.Command{
		Use:   "run",
		Short: "Run the agent in the foreground",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAgent(*opts)
		},
	})

	return cmd
}

func newAgentServiceCommand(action string, opts *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use:   action,
		Short: action + " the native service",
		RunE: func(cmd *cobra.Command, _ []string) error {
			runtime, err := buildAgentService(*opts)
			if err != nil {
				return err
			}
			return kservice.Control(runtime.service, action)
		},
	}
}

func runAgent(opts cliOptions) error {
	runtime, err := buildAgentService(opts)
	if err != nil {
		return err
	}
	return runtime.service.Run()
}

func buildAgentService(opts cliOptions) (*serviceRuntime, error) {
	upstreams := agent.ParseUpstreams(opts.upstream)
	if len(upstreams) == 0 {
		return nil, fmt.Errorf("at least one --upstream must be provided")
	}

	interactive := kservice.Interactive()
	logger := appservice.NewLogger(interactive)
	runner := agent.New(agent.Options{
		Upstreams:        upstreams,
		RegisterPath:     opts.registerPath,
		RequestTimeout:   opts.requestTimeout,
		InitialHeartbeat: opts.initialHeartbeat,
	}, logger)
	program := appservice.NewProgram(runner, logger)

	args := []string{"run"}
	for _, upstream := range opts.upstream {
		args = append(args, "--upstream", upstream)
	}
	args = append(args,
		"--register-path", opts.registerPath,
		"--request-timeout", opts.requestTimeout.String(),
		"--initial-heartbeat", opts.initialHeartbeat.String(),
	)

	svc, err := kservice.New(program, &kservice.Config{
		Name:        "wol-agent",
		DisplayName: "wol-agent",
		Description: "Wake-on-LAN heartbeat agent",
		Arguments:   args,
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

	return &serviceRuntime{service: svc, program: program, logger: logger}, nil
}
