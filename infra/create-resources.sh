#!/usr/bin/env bash
# Creates the AWS resources step by step. Every step is idempotent (safe to re-run).
# Usage: infra/create-resources.sh <step>     steps: table | photos-bucket | all
source "$(dirname "$0")/config.sh"

# ---- 5.1a DynamoDB table: on-demand, PK/SK strings ----
step_table() {
  say "DynamoDB table $TABLE"
  if aws dynamodb describe-table --table-name "$TABLE" >/dev/null 2>&1; then
    echo "already exists"
  else
    aws dynamodb create-table \
      --table-name "$TABLE" \
      --attribute-definitions AttributeName=PK,AttributeType=S AttributeName=SK,AttributeType=S \
      --key-schema AttributeName=PK,KeyType=HASH AttributeName=SK,KeyType=RANGE \
      --billing-mode PAY_PER_REQUEST \
      --tags "$TAGS_KV" \
      --query 'TableDescription.TableStatus' --output text
    aws dynamodb wait table-exists --table-name "$TABLE"   # CREATING → ACTIVE
  fi
  aws dynamodb describe-table --table-name "$TABLE" \
    --query 'Table.{status:TableStatus,billing:BillingModeSummary.BillingMode,arn:TableArn}' --output table
}

# ---- 5.1b Private S3 bucket for photos, with CORS for browser uploads ----
step_photos_bucket() {
  say "S3 photo bucket $PHOTO_BUCKET"
  if aws s3api head-bucket --bucket "$PHOTO_BUCKET" >/dev/null 2>&1; then
    echo "already exists"
  else
    # Outside us-east-1, the region must be given as a LocationConstraint.
    aws s3api create-bucket --bucket "$PHOTO_BUCKET" \
      --create-bucket-configuration LocationConstraint="$REGION" --output text >/dev/null
  fi
  aws s3api put-bucket-tagging --bucket "$PHOTO_BUCKET" --tagging "TagSet=[{Key=project,Value=$PROJECT}]"
  # Private: block every form of public access (new buckets default to this; we make it explicit).
  aws s3api put-public-access-block --bucket "$PHOTO_BUCKET" --public-access-block-configuration \
    BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true
  # CORS: the browser PUTs/GETs photos directly with presigned URLs, from the local page or the S3 website.
  aws s3api put-bucket-cors --bucket "$PHOTO_BUCKET" --cors-configuration "{
    \"CORSRules\": [{
      \"AllowedOrigins\": [\"http://localhost:5500\", \"$WEB_ORIGIN\"],
      \"AllowedMethods\": [\"PUT\", \"GET\"],
      \"AllowedHeaders\": [\"*\"],
      \"MaxAgeSeconds\": 3000
    }]}"
  echo "public access block:"; aws s3api get-public-access-block --bucket "$PHOTO_BUCKET" \
    --query 'PublicAccessBlockConfiguration' --output table
  echo "CORS origins:"; aws s3api get-bucket-cors --bucket "$PHOTO_BUCKET" --query 'CORSRules[0].AllowedOrigins' --output text
}

case "${1:-}" in
  table)         step_table ;;
  photos-bucket) step_photos_bucket ;;
  all)           step_table; step_photos_bucket ;;
  *) echo "usage: $0 table|photos-bucket|all"; exit 1 ;;
esac
