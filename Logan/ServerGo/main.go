package main

import (
	"context"
	"log/slog"
	"logan/servergo/internal/config"
	"os"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	c, err := config.Load(os.Getenv)
	if err != nil {
		slog.Error("configuration rejected", "reason", err.Error())
		os.Exit(1)
	}
	a, err := newApplication(context.Background(), c)
	if err != nil {
		slog.Error("startup failed", "reason", err.Error())
		os.Exit(1)
	}
	slog.Info("starting Logan Go service", "address", c.Address, "context_path", c.ContextPath, "business_ready", false)
	if err := a.Run(); err != nil {
		slog.Error("server stopped with error")
		os.Exit(1)
	}
}
