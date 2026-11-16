// Entry point 2: AWS Lambda. No port: pulls events from the Lambda Runtime API.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-lambda-go/lambdacontext"
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
	adapter := httpadapter.NewV2(withLambdaRequestID(h))
	lambda.Start(adapter.ProxyWithContext) // pull loop: "next event?" → handle → "result"
}

// withLambdaRequestID makes our log lines use Lambda's request ID (the one on START/END/REPORT),
// so one ID ties all of a request's log lines together. chi's RequestID middleware reuses an
// incoming X-Request-Id header; we set it here, overwriting any client-sent value (could be spoofed).
// The ID is also returned to the client, so a bug report can quote it.
func withLambdaRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if lc, ok := lambdacontext.FromContext(r.Context()); ok {
			r.Header.Set("X-Request-Id", lc.AwsRequestID)
			w.Header().Set("X-Request-Id", lc.AwsRequestID)
		}
		next.ServeHTTP(w, r)
	})
}
