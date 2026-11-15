// Entry point 2: AWS Lambda. No port: pulls events from the Lambda Runtime API.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"notes-app/internal/app"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil))) // stdout → CloudWatch Logs
	// Init runs once per cold start; warm invocations reuse h and its AWS clients.
	h, err := app.NewHandler(context.Background())
	if err != nil {
		slog.Error("startup", "err", err)
		os.Exit(1)
	}
	// Translator: API Gateway HTTP API (payload v2) event JSON → *http.Request → chi → response JSON.
	adapter := httpadapter.NewV2(h)
	lambda.Start(adapter.ProxyWithContext) // pull loop: "next event?" → handle → "result"
}
