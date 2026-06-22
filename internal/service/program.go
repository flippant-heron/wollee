package appservice

import (
	"context"
	"errors"
	"sync"
	"time"

	kservice "github.com/kardianos/service"
)

type Runner interface {
	Run(context.Context) error
	Shutdown(context.Context) error
}

type Program struct {
	mu     sync.Mutex
	runner Runner
	logger *Logger
	cancel context.CancelFunc
	done   chan struct{}
}

func NewProgram(runner Runner, logger *Logger) *Program {
	return &Program{
		runner: runner,
		logger: logger,
	}
}

func (p *Program) Start(_ kservice.Service) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.runner == nil {
		return errors.New("runner is required")
	}

	if p.done != nil {
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan struct{})

	go func(done chan struct{}) {
		defer close(done)
		if err := p.runner.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			p.logger.Error("service stopped with error", err)
		}
	}(p.done)

	return nil
}

func (p *Program) Stop(_ kservice.Service) error {
	p.mu.Lock()
	cancel := p.cancel
	done := p.done
	p.cancel = nil
	p.done = nil
	p.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	shutdownCtx, release := context.WithTimeout(context.Background(), 10*time.Second)
	defer release()

	err := p.runner.Shutdown(shutdownCtx)
	if done == nil {
		return err
	}

	select {
	case <-done:
	case <-shutdownCtx.Done():
		if err == nil {
			err = shutdownCtx.Err()
		}
	}

	return err
}
