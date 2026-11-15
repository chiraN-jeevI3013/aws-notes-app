// Package app wires config, AWS clients, store, photos and the router.
// Shared by every entry point (cmd/server, cmd/lambda), so they differ only in how requests arrive.
package app

import (
	"context"
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

// Env returns the env var k, or def if unset.
func Env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// NewHandler builds the full HTTP handler (chi router + dependencies) from env vars.
func NewHandler(ctx context.Context) (http.Handler, error) {
	endpoint := os.Getenv("DYNAMODB_ENDPOINT")        // e.g. http://localhost:8000 for DynamoDB Local
	s3Endpoint := os.Getenv("S3_ENDPOINT")            // e.g. http://localhost:4566 for LocalStack
	s3Public := Env("S3_PUBLIC_ENDPOINT", s3Endpoint) // host the browser uses for presigned URLs
	opts := []func(*config.LoadOptions) error{config.WithRegion(Env("AWS_REGION", "us-east-2"))}
	if endpoint != "" || s3Endpoint != "" {
		// Local emulators: dummy creds so real ones are never used. LocalStack verifies
		// presigned-URL signatures with the secret "test", so the dummies must be test/test.
		opts = append(opts, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")))
	}
	// On Lambda no endpoints are set: real AWS endpoints, and the execution role's
	// temporary creds (injected as env vars) are found by the default provider chain.
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
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

	st := store.New(db, Env("TABLE_NAME", "notes-app"))
	if endpoint != "" { // local only; in AWS the table is pre-created and the role can't create tables
		if err := st.EnsureTable(ctx); err != nil {
			return nil, err
		}
	}
	ph := photos.New(newS3(s3Endpoint), newS3(s3Public), Env("PHOTO_BUCKET", "notes-app-photos"))
	return api.NewRouter(&api.Server{Store: st, Photos: ph}, Env("ALLOWED_ORIGIN", "*")), nil
}
