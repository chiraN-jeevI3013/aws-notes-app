#!/usr/bin/env bash
# Shared settings for all infra scripts. Sourced, not run directly.
set -euo pipefail
export AWS_PAGER=""                       # don't open "less" for CLI output

REGION="us-east-2"                        # an SCP in this account denies every other region
PROJECT="notes-app"
TAGS_KV="Key=project,Value=$PROJECT"      # tag every resource so teardown can find it

# Guard: fake LocalStack creds left in the shell would override `aws login` (env vars win).
if [[ "${AWS_ACCESS_KEY_ID:-}" == "test" || "${AWS_ACCESS_KEY_ID:-}" == "local" ]]; then
  echo "ERROR: AWS_ACCESS_KEY_ID=$AWS_ACCESS_KEY_ID is a fake local credential. Run:" >&2
  echo "  unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN" >&2
  exit 1
fi

# Looked up at runtime so the account ID is never hard-coded in the (public) repo.
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)

TABLE="notes-app"
# S3 names are globally unique (they become DNS hostnames). Override via env if these are taken,
# e.g. PHOTO_BUCKET=notes-app-photos-$ACCOUNT_ID infra/create-resources.sh all
PHOTO_BUCKET="${PHOTO_BUCKET:-notes-app-photos-cj}"
WEB_BUCKET="${WEB_BUCKET:-notes-app-web-cj}"
WEB_ORIGIN="http://$WEB_BUCKET.s3-website.$REGION.amazonaws.com"   # S3 static website URL (step 5.6)
ROLE_NAME="notes-app-lambda-role"
FUNCTION_NAME="notes-app-api"
API_NAME="notes-app-http-api"
PIPELINE_NAME="notes-app-pipeline"
BUILD_PROJECT="notes-app-build"

say() { printf '\n==> %s\n' "$*"; }
