package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dsri45/gatekeeper/internal/config"
	"github.com/dsri45/gatekeeper/internal/gateway"
	"github.com/dsri45/gatekeeper/internal/limiter"
	applicationmetrics "github.com/dsri45/gatekeeper/internal/metrics"
)

const maxHeaderBytes = 1 << 20 // 1 MiB

type options struct {
	configPath string
}

type idleConnectionCloser interface {
	CloseIdleConnections()
}

func main() {
	shutdownContext, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(shutdownContext, os.Args[1:], logger); err != nil {
		logger.Error("gateway stopped with an error", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string, logger *slog.Logger) error {
	options, err := parseOptions(arguments)
	if err != nil {
		return err
	}

	cfg, err := config.Load(options.configPath)
	if err != nil {
		return err
	}

	redisLimiter, err := limiter.NewRedis(cfg.Redis)
	if err != nil {
		return fmt.Errorf("create Redis limiter: %w", err)
	}
	defer func() {
		if closeErr := redisLimiter.Close(); closeErr != nil {
			logger.Warn("close Redis limiter", "error", closeErr)
		}
	}()

	application, err := gateway.New(cfg, redisLimiter, redisLimiter, applicationmetrics.New(), logger)
	if err != nil {
		return err
	}

	server := newHTTPServer(cfg.Server, application)
	return serve(ctx, server, application, cfg.Server.ShutdownTimeout.Duration, logger)
}

func parseOptions(arguments []string) (options, error) {
	flags := flag.NewFlagSet("gateway", flag.ContinueOnError)
	configPath := flags.String("config", "config/config.example.yaml", "path to the gateway YAML configuration")

	if err := flags.Parse(arguments); err != nil {
		return options{}, fmt.Errorf("parse command-line flags: %w", err)
	}
	if flags.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}

	return options{configPath: *configPath}, nil
}

func newHTTPServer(serverConfig config.ServerConfig, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              serverConfig.Address,
		Handler:           handler,
		ReadHeaderTimeout: serverConfig.ReadHeaderTimeout.Duration,
		ReadTimeout:       serverConfig.ReadTimeout.Duration,
		WriteTimeout:      serverConfig.WriteTimeout.Duration,
		IdleTimeout:       serverConfig.IdleTimeout.Duration,
		MaxHeaderBytes:    maxHeaderBytes,
	}
}

func serve(
	ctx context.Context,
	server *http.Server,
	closer idleConnectionCloser,
	shutdownTimeout time.Duration,
	logger *slog.Logger,
) error {
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return fmt.Errorf("listen on %q: %w", server.Addr, err)
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("gateway listening", "address", listener.Addr().String())
		serverErrors <- server.Serve(listener)
	}()

	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)

	case <-ctx.Done():
		logger.Info("gateway shutdown started")
		shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := server.Shutdown(shutdownContext); err != nil {
			_ = listener.Close()
			return fmt.Errorf("shutdown HTTP server: %w", err)
		}
		closer.CloseIdleConnections()

		err := <-serverErrors
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP after shutdown: %w", err)
		}
		logger.Info("gateway shutdown complete")
		return nil
	}
}
