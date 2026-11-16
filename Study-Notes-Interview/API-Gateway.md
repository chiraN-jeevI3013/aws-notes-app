# API Gateway

## What it is
- A managed, always-on **HTTPS front door**. It receives client requests, applies routing, auth, throttling and CORS, invokes a backend (Lambda, HTTP service, AWS service), and returns the response.
- With Lambda: HTTP request → **event JSON** → Lambda invoked **synchronously** → response JSON → HTTP response.

## "REST API" the product vs REST the style
- **REST (style)** = how an API is designed: resources, verbs, status codes. Our notes API is RESTful.
- **"REST API" (console)** = the **name of API Gateway v1**, a product. **"HTTP API"** = v2.
- Both can serve a RESTful API. AWS describes HTTP API as "low-latency and cost-effective REST APIs".

## The 4 API types
| Type | What it is | Use when | Example |
|---|---|---|---|
| **HTTP API** (v2) | lean proxy to Lambda/HTTP backends; ~$1/million requests; lower latency; built-in JWT/OAuth2 authorizers; simple CORS | most new APIs in front of Lambda or a service | **our notes API**, mobile backends |
| **REST API** (v1) | feature-rich: **API keys + usage plans** (per-customer quotas), request **validation**, request/response **transformations**, **caching**, WAF, X-Ray, direct AWS service integrations (e.g. → SQS, no Lambda); ~$3.50/million | selling an API with per-customer keys/quotas; need caching or transforms | public weather API with free/paid tiers |
| **REST API Private** | REST v1 reachable **only from inside a VPC** | internal microservices | payroll service called by internal services |
| **WebSocket API** | **persistent two-way** connection; server can push anytime | real-time | chat, live dashboards, games, notifications |
- **Why HTTP API here:** chi already does routing, validation and CORS, so v1's extras add nothing. HTTP API = same result, ~70% cheaper, faster.

## Key concepts
- **Route** = method + path → integration. We use one catch-all **`$default`** route, so chi does all routing (one router, testable locally).
- **Integration** = the backend. Ours: Lambda `notes-app-api`, **payload format 2.0** (must match `httpadapter.NewV2` in `cmd/lambda`).
- **Stage** = a deployed version (`dev`, `prod`). We use `$default` + **auto-deploy** → clean URL with no `/prod` prefix.
- **`$default` route has no method:** it means *any method + any path not matched by another route* (incl. `/`). Compare `ANY /{proxy+}`: any method, any path *below* `/`, but not `/` itself.
- **Stages in detail:** route/integration edits go into a *draft*; a **deployment** snapshots it; a **stage** serves a snapshot at a URL (`$default` → no prefix; `dev` → `/dev/...`; `prod` → `/prod/...`). Each stage has its own throttling, logs and **stage variables** (e.g. point `dev` at a Lambda alias `dev`).
  - **Auto-deploy ON** = every saved change goes live immediately (dev/learning). **OFF** = changes wait until you click Deploy (prod: no half-finished edits reaching users; usually CI/CD deploys).
  - We use `$default` + auto-deploy: paths reach chi unchanged (`/folders`, not `/prod/folders`).
- **Resource-based policy** (on the Lambda) = **who may invoke it** ("API Gateway may call me"). Compare: the execution role = what the Lambda may do. The console adds this policy automatically when you attach the integration.
- **CORS:** left OFF in API Gateway. If on, API Gateway answers `OPTIONS` itself and overrides our middleware. Off → preflights pass to chi, exactly like local.

## The resource-based policy the console added (on the Lambda)
| Field | Value | Meaning |
|---|---|---|
| Principal | `apigateway.amazonaws.com` | the API Gateway service |
| Action | `lambda:InvokeFunction` | may call the function |
| Resource | `...function:notes-app-api` | this function only |
| Condition `AWS:SourceArn` | `arn:aws:execute-api:us-east-2:<acct>:<apiId>/*/$default` | only when the call comes from **our** API (any stage, route `$default`) |
- Without the condition, any API Gateway (any account) could try to invoke it. Scoping a service principal with `SourceArn` = **confused deputy protection**.
- Find it: Lambda → Configuration → Permissions → Resource-based policy statements. The Lambda diagram shows "API Gateway" as a **trigger**.
- **Invoke URL:** API Gateway → APIs → your API → Stages table (or Deploy → Stages → `$default`). CLI: `infra/create-resources.sh api`.
- **Account throttling** (5000 burst / 10000 rate) = AWS's regional ceiling across all your APIs; route throttling (ours 20/10) sits under it.

## IPv4 vs Dualstack
- IPv6 is **not more secure**; it's just a bigger address space (IPv4 ≈ 4 billion addresses, running out). Security = HTTPS, auth, IAM.
- IPv4 = only IPv4 clients; Dualstack = IPv4 + IPv6 clients. We use IPv4 (universal). Changeable later without redeploying.

## Throttling: burst vs rate (token bucket)
- **Burst limit** = bucket size = max requests **in one instant**.
- **Rate limit** = refill speed (tokens/sec) = **sustained** throughput.
- Ours: burst 20, rate 10/s. 20 simultaneous requests pass, the 21st gets **429 Too Many Requests**, then ~10/s keep passing.
- Why: rejected requests never reach Lambda → a bot hammering the public URL can't run up the bill.

## Interview one-liners
- "I chose an HTTP API over a REST API: I didn't need API keys, usage plans, or caching, and HTTP API is ~70% cheaper with lower latency."
- "One `$default` route proxies everything to Lambda; routing lives in code (chi), so it's testable locally."
- "Throttling is a token bucket: burst absorbs spikes, rate caps steady throughput; excess gets 429 before Lambda runs."
- "Two policies: the execution role says what my Lambda can do; the resource-based policy says who can invoke it."

## My real verification (2026-11-15)
- `curl -i $API/health` → `HTTP/2 200`, our CORS headers, plus `apigw-requestid` (API Gateway's own request ID).
- `BASE_URL=$API scripts/smoke.sh` → 13/13 CRUD checks pass against real Lambda + DynamoDB.
- `BASE_URL=$API scripts/photo-test.sh` → 9/9 pass. **Real S3** returned 403 for a wrong Content-Type: SigV4 signature enforcement works for real.
- Presigned URLs became virtual-hosted style (`https://<bucket>.s3.us-east-2.amazonaws.com/<key>`) vs LocalStack's path style (`localhost:4566/<bucket>/<key>`). Same code, no endpoint override.
