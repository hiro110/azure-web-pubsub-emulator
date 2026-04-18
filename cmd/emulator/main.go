package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/hiro110/azure-web-pubsub-emulator/internal/config"
	"github.com/hiro110/azure-web-pubsub-emulator/internal/server"
)

// version is set at build time via -ldflags.
var version = "dev"

var (
	flagConfig    string
	flagPort      int
	flagAccessKey string
	flagLogLevel  string
)

var rootCmd = &cobra.Command{
	Use:     "web-pubsub-emulator",
	Short:   "Azure Web PubSub local emulator",
	Version: version,
	RunE:    run,
}

func init() {
	rootCmd.Flags().StringVarP(&flagConfig, "config", "c", "config.yaml", "Path to config file")
	rootCmd.Flags().IntVarP(&flagPort, "port", "p", 0, "Server port (overrides config)")
	rootCmd.Flags().StringVar(&flagAccessKey, "access-key", "", "Access key (overrides config)")
	rootCmd.Flags().StringVar(&flagLogLevel, "log-level", "", "Log level: debug|info|warn|error")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(flagConfig)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	cfg.ApplyFlags(flagPort, flagAccessKey, flagLogLevel)

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	logger, err := buildLogger(cfg.Logging)
	if err != nil {
		return fmt.Errorf("building logger: %w", err)
	}
	defer logger.Sync() //nolint:errcheck

	srv, err := server.New(cfg, logger)
	if err != nil {
		return fmt.Errorf("creating server: %w", err)
	}
	return srv.Run()
}

func buildLogger(cfg config.LoggingConfig) (*zap.Logger, error) {
	var zapCfg zap.Config
	if cfg.Level == "debug" {
		zapCfg = zap.NewDevelopmentConfig()
	} else {
		zapCfg = zap.NewProductionConfig()
	}

	level, err := zap.ParseAtomicLevel(cfg.Level)
	if err != nil {
		return nil, fmt.Errorf("invalid log level %q: %w", cfg.Level, err)
	}
	zapCfg.Level = level

	if cfg.Format == "text" {
		zapCfg.Encoding = "console"
	} else {
		zapCfg.Encoding = "json"
	}

	return zapCfg.Build()
}
