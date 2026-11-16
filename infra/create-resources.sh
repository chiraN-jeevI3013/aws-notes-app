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

# ---- 5.2 IAM role for the Lambda ----
# The policy file uses placeholders so the public repo has no account ID. This fills them in.
render_policy() {
  sed -e "s/ACCOUNT_ID/$ACCOUNT_ID/g" -e "s/PHOTO_BUCKET/$PHOTO_BUCKET/g" \
    "$(dirname "$0")/iam/lambda-permissions.json"
}

step_role() {
  say "IAM role $ROLE_NAME"
  if ! aws iam get-role --role-name "$ROLE_NAME" >/dev/null 2>&1; then
    aws iam create-role --role-name "$ROLE_NAME" \
      --assume-role-policy-document "file://$(dirname "$0")/iam/lambda-trust.json" \
      --tags "$TAGS_KV" --query 'Role.Arn' --output text
  fi
  aws iam put-role-policy --role-name "$ROLE_NAME" --policy-name notes-app-access \
    --policy-document "$(render_policy)"
  echo "trusted service:"; aws iam get-role --role-name "$ROLE_NAME" \
    --query 'Role.AssumeRolePolicyDocument.Statement[0].Principal.Service' --output text
  echo "inline policies:"; aws iam list-role-policies --role-name "$ROLE_NAME" --query 'PolicyNames' --output text
  echo "managed policies (should be empty):"; aws iam list-attached-role-policies --role-name "$ROLE_NAME" \
    --query 'AttachedPolicies[].PolicyName' --output text
}

# ---- 5.4 API Gateway HTTP API (created in the console; this step only verifies) ----
step_api() {
  say "API Gateway HTTP API $API_NAME"
  API_ID=$(aws apigatewayv2 get-apis --query "Items[?Name=='$API_NAME'].ApiId | [0]" --output text)
  [[ "$API_ID" == "None" ]] && { echo "not found: create it in the console"; exit 1; }
  aws apigatewayv2 get-routes --api-id "$API_ID" --query 'Items[].RouteKey' --output text | sed 's/^/routes: /'
  aws apigatewayv2 get-integrations --api-id "$API_ID" \
    --query 'Items[].[IntegrationType,PayloadFormatVersion]' --output text | sed 's/^/integration: /'
  echo "cors (should be empty, our middleware does CORS): $(aws apigatewayv2 get-api --api-id "$API_ID" --query 'CorsConfiguration' --output text)"
  echo "API_URL=$(aws apigatewayv2 get-api --api-id "$API_ID" --query ApiEndpoint --output text)"
}

# ---- 5.5 Public S3 website bucket for the frontend (created in the console; this step verifies) ----
step_web_bucket() {
  say "S3 website bucket $WEB_BUCKET"
  aws s3api head-bucket --bucket "$WEB_BUCKET" >/dev/null 2>&1 || { echo "not found: create it in the console"; exit 1; }
  echo "website index: $(aws s3api get-bucket-website --bucket "$WEB_BUCKET" --query 'IndexDocument.Suffix' --output text)"
  echo "public access block:"; aws s3api get-public-access-block --bucket "$WEB_BUCKET" \
    --query 'PublicAccessBlockConfiguration' --output table 2>/dev/null || echo "  none (all off)"
  echo "policy: $(aws s3api get-bucket-policy --bucket "$WEB_BUCKET" --query Policy --output text)"
  echo "files:"; aws s3 ls "s3://$WEB_BUCKET/"
  echo "HTTP status of $WEB_ORIGIN: $(curl -s -o /dev/null -w '%{http_code}' "$WEB_ORIGIN")"
}

case "${1:-}" in
  table)         step_table ;;
  photos-bucket) step_photos_bucket ;;
  role)          step_role ;;
  print-policy)  render_policy ;;
  api)           step_api ;;
  web-bucket)    step_web_bucket ;;
  all)           step_table; step_photos_bucket; step_role ;;
  *) echo "usage: $0 table|photos-bucket|role|print-policy|api|web-bucket|all"; exit 1 ;;
esac
