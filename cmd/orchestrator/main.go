package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-logr/zapr"
	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/geoberle/kelos/cmd/options"
)

func main() {
	zapLogger, err := zap.NewProduction()
	if err != nil {
		os.Exit(1)
	}
	logger := zapr.NewLogger(zapLogger)
	raw := &options.RawOptions{}
	command := &cobra.Command{Use: "orchestrator", SilenceUsage: true, SilenceErrors: true, RunE: func(command *cobra.Command, _ []string) error {
		validated, err := raw.Validate()
		if err != nil {
			return err
		}
		completed, err := validated.Complete()
		if err != nil {
			return err
		}
		return completed.Run(command.Context(), logger)
	}}
	command.Flags().StringVar(&raw.ConfigFile, "config-file", "", "Versioned configuration JSON")
	command.Flags().StringVar(&raw.TokenFile, "token-file", "", "GitHub credential file")
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := command.ExecuteContext(ctx); err != nil {
		logger.Error(err, "Orchestrator failed")
		os.Exit(1)
	}
}
