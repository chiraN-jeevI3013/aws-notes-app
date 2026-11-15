#!/usr/bin/env bash
# Builds the Lambda zip: a static Linux/arm64 binary named "bootstrap" (what provided.al2023 runs).
set -euo pipefail
cd "$(dirname "$0")/.."
rm -rf dist && mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags lambda.norpc -ldflags="-s -w" -o dist/bootstrap ./cmd/lambda
(cd dist && zip -q notes-app-api.zip bootstrap)
ls -lh dist/notes-app-api.zip | awk '{print "built dist/notes-app-api.zip (" $5 ")"}'
