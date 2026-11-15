#!/bin/bash
# Runs inside the LocalStack container once it's ready (local dev only).
# awslocal = the aws CLI pre-pointed at LocalStack.
awslocal s3 mb s3://notes-app-photos

# Bucket CORS: lets the page on http://localhost:5500 PUT/GET photos directly
# with presigned URLs. Phase 5 sets the same rule on the real bucket.
awslocal s3api put-bucket-cors --bucket notes-app-photos --cors-configuration '{
  "CORSRules": [{
    "AllowedOrigins": ["http://localhost:5500"],
    "AllowedMethods": ["PUT", "GET"],
    "AllowedHeaders": ["*"],
    "MaxAgeSeconds": 3000
  }]
}'
