// Local HTTP server. Same router as the Lambda entrypoint.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"notes-app/internal/api"
	"notes-app/internal/photos"
	"notes-app/internal/store"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	ctx := context.Background()

	endpoint := os.Getenv("DYNAMODB_ENDPOINT") // e.g. http://localhost:8000 for DynamoDB Local
	s3Endpoint := os.Getenv("S3_ENDPOINT")     // e.g. http://localhost:4566 for LocalStack
	s3Public := env("S3_PUBLIC_ENDPOINT", s3Endpoint) // host the browser uses for presigned URLs
	opts := []func(*config.LoadOptions) error{config.WithRegion(env("AWS_REGION", "us-east-2"))}
	if endpoint != "" || s3Endpoint != "" {
		// Local emulators: dummy creds so real ones are never used. LocalStack verifies
		// presigned-URL signatures with the secret "test", so the dummies must be test/test.
		opts = append(opts, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		slog.Error("aws config", "err", err)
		os.Exit(1)
	}
	db := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
	})

	newS3 := func(endpoint string) *s3.Client {
		return s3.NewFromConfig(cfg, func(o *s3.Options) {
			if endpoint != "" {
				o.BaseEndpoint = aws.String(endpoint)
				o.UsePathStyle = true // http://host:4566/<bucket>/<key> instead of <bucket>.host
			}
		})
	}
	s3c, s3pub := newS3(s3Endpoint), newS3(s3Public)

	st := store.New(db, env("TABLE_NAME", "notes-app"))
	if endpoint != "" {
		if err := st.EnsureTable(ctx); err != nil {
			slog.Error("ensure table", "err", err)
			os.Exit(1)
		}
	}

	addr := ":" + env("PORT", "8080")
	slog.Info("listening", "addr", addr)
	h := api.NewRouter(&api.Server{Store: st, Photos: photos.New(s3c, s3pub, env("PHOTO_BUCKET", "notes-app-photos"))}, env("ALLOWED_ORIGIN", "*"))
	if err := http.ListenAndServe(addr, h); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}
