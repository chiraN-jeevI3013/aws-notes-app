// Entry point 1: long-running HTTP server (Mac, Docker, ECS). Listens on a port.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"notes-app/internal/app"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	h, err := app.NewHandler(context.Background())
	if err != nil {
		slog.Error("startup", "err", err)
		os.Exit(1)
	}
	addr := ":" + app.Env("PORT", "8080")
	slog.Info("listening", "addr", addr)
	if err := http.ListenAndServe(addr, h); err != nil { // push: wait for connections
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}
