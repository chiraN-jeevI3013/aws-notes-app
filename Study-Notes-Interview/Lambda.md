# AWS Lambda

## What it is
- **Functions as a Service:** you upload code; AWS runs it **only when an event arrives** (HTTP request, S3 upload, queue message, cron…). No servers to manage, no idle cost. You pay per request + per ms of run time.
- Each instance runs in a **Firecracker micro-VM**. One instance handles **one request at a time**. 100 concurrent requests → AWS runs ~100 instances.
- **Cold start:** no warm instance → AWS boots a micro-VM and runs your `main()` (Go: ~100–300 ms). **Warm start:** reuses an existing instance, skipping init. Instances are frozen between requests and eventually recycled → **must be stateless** (see AWS-Compute-Hosting.md).

## Who is awake? (common misconception)
- It's NOT "something of ours stays awake to wake up API Gateway".
- **API Gateway and the Lambda service are always on.** They're AWS-managed fleets serving every customer. You pay per request, not for idle.
- Only **our code** sleeps. It's started on demand.
```
Browser ──HTTPS──► API Gateway (always on, AWS-run, listens on :443)
                     │ "request for notes-app-api"
                     ▼
                   Lambda service (always on, AWS-run)
                     │ warm instance? → reuse   none? → boot micro-VM + run bootstrap (cold start)
                     ▼
                   our binary: adapter → chi router → handler → DynamoDB/S3
```

## Why a server with a port doesn't work on Lambda
- On Mac / Docker / ECS / EC2 our process runs 24/7 and **listens on a port** (`http.ListenAndServe(":8080")`). Requests come to it.
- On Lambda **nothing of ours listens on a port.** API Gateway owns the public port. Our binary **pulls** work: the `aws-lambda-go` library loops on the local **Runtime API** ("give me the next invocation" → handle → "here's the result"). That loop is what `lambda.Start(...)` runs.
- Push vs pull: a server waits for connections (push). A Lambda asks for the next event (pull).

## Events: JSON in, JSON out
API Gateway (HTTP API, payload v2) turns the browser's HTTP request into an **event** (simplified):
```json
{ "rawPath": "/folders",
  "requestContext": { "http": { "method": "GET" } },
  "headers": { "origin": "http://notes-app-web-cj.s3-website.us-east-2.amazonaws.com" },
  "body": "", "isBase64Encoded": false }
```
and expects back:
```json
{ "statusCode": 200, "headers": { "content-type": "application/json" }, "body": "[...]" }
```

## The adapter (translator) and where chi fits
- chi only speaks Go's `http.Handler`: `ServeHTTP(w http.ResponseWriter, r *http.Request)`. The whole `net/http` ecosystem uses that interface.
- `aws-lambda-go-api-proxy` bridges the gap:
```
event JSON ─► build *http.Request (in memory) ─► router.ServeHTTP(w, req) ─► read status/headers/body ─► response JSON
```
- **No network hop inside Lambda.** It's a direct function call. chi, middleware (request ID, logging, CORS) and handlers have no idea they're on Lambda.
- Alternative without an adapter: write handlers against `events.APIGatewayV2HTTPRequest` directly. Faster to start, but it locks your code to Lambda and loses chi/middleware. The adapter keeps the code portable.

## Entry points: why there are now two
- An **entry point** = a `package main` with `func main()`, where the program starts. Go convention: one folder per binary under `cmd/`.
- `main()` = wiring: read env config → build AWS clients → build store/photos → build router → **start receiving requests**. Only the last step depends on the platform.
- Until now there was one way to run the app, so there was one entry point. Lambda needs a different "start receiving" step, so we add one. We keep the old one for local/Docker (and ECS later).
```
cmd/server/main.go  → http.ListenAndServe(":8080", router)   (Mac, Docker, ECS/EC2)
cmd/lambda/main.go  → lambda.Start(adapter(router))          (Lambda)
        both ──► internal/api (chi) ──► internal/store (DynamoDB), internal/photos (S3)
```
- **One repo, two binaries, same business logic.** This is the *ports and adapters* (hexagonal) idea: the core doesn't know how it's hosted.

## Now (local) vs deployed (Lambda): what changes
| Layer | Now (Mac / Docker) | On AWS (Lambda) | Changed? |
|---|---|---|---|
| Frontend hosting | `python -m http.server` :5500 | S3 static website bucket | new host |
| Frontend code | `API_URL = http://localhost:8080` | `API_URL = https://<id>.execute-api...` | one line |
| Front door | none (browser → Go directly) | **API Gateway** (HTTPS, CORS, throttling) | new |
| Who listens on a port | our Go process, :8080 | API Gateway, :443 | moved to AWS |
| Entry point | `cmd/server/main.go` | `cmd/lambda/main.go` | new file |
| Translator | not needed | `aws-lambda-go-api-proxy` | new |
| chi router, middleware, handlers | `internal/api` | **same code** | unchanged |
| DB / S3 code | `internal/store`, `internal/photos` | **same code** | unchanged |
| DynamoDB | DynamoDB Local container | table `notes-app` | config only |
| S3 | LocalStack | bucket `notes-app-photos-cj` | config only |
| Credentials | fake `test/test` | execution role `notes-app-lambda-role` (auto) | config only |
| Table creation | `EnsureTable` at startup | skipped (made in console; role can't create tables) | skipped |
| Logs | `docker compose logs` | CloudWatch Logs `/aws/lambda/notes-app-api` | destination |
| Packaging | Docker image (23 MB) | `bootstrap` binary in a zip | different |

## Console choice: Event function vs Web Function
| | Event function (what we use) | Web Function |
|---|---|---|
| Your code | handles **event JSON** (`lambda.Start`) | runs an **HTTP server listening on a port** |
| Front door | a trigger: API Gateway, S3, SQS, cron… | its own built-in **endpoint(s)** |
| Our project | `cmd/lambda` + adapter, behind API Gateway | would be `cmd/server` almost unchanged |
| Good for | any event source; API GW features (routes, throttling, authorizers) | porting an existing web app quickly |
- Why Event here: it's the classic pattern (API Gateway → Lambda), and it works for non-HTTP triggers too.
- Similar older idea: **Lambda Web Adapter** (a layer that turns events into HTTP calls to your port).

## Creating the function: console settings we chose
| Setting | Choice | Why |
|---|---|---|
| ARM64 architecture | **ON** | Must match the binary (`GOARCH=arm64`) |
| Custom execution role | **ON** → existing `notes-app-lambda-role` | Off = Lambda auto-creates a role without our DynamoDB/S3 permissions |
| Tags | **ON** → `project=notes-app` | Teardown and cost tracking find resources by tag |
| Durable execution | off | Checkpointed multi-step workflows; **can't be changed later** |
| EC2 capacity provider | off | Runs Lambda on EC2 instance types you pick; we want normal serverless compute |
| Function URL | off | Built-in HTTPS URL; API Gateway is our front door instead |
| Tenant isolation mode | off | Separate environment per tenant (multi-tenant SaaS); can't be changed later |
| VPC | off | Only for private resources (RDS, ElastiCache). DynamoDB/S3 are public AWS APIs; a VPC adds NAT/endpoint complexity |
| Code signing / KMS | off | Enterprise controls; default encryption is already on |

## Why arm64?
- **Must match the binary:** we compile with `GOARCH=arm64`. An x86_64 function would fail with `exec format error`. The CPU architecture is the binary's "language".
- **Cost:** arm64 runs on AWS **Graviton** (AWS-designed ARM CPUs): ~**20% cheaper per GB-second** than x86, with similar or better performance for Go.
- **Free with Go:** switching is one build env var (`GOARCH`); Go cross-compiles from any machine. Apple Silicon Macs are ARM too.
- **When x86 instead?** Dependencies with x86-only native code (some C libraries/CGO, old binaries).
- **Interview line:** "I deploy Lambdas on arm64/Graviton for ~20% lower cost; Go cross-compiles with `GOARCH=arm64`, so it's just a build flag."

## Packaging for the `provided.al2023` runtime
- Go has no "managed" Lambda runtime anymore. You use the **OS-only runtime** `provided.al2023` (Amazon Linux 2023) and upload a compiled binary named exactly **`bootstrap`**. Lambda executes that file.
- Build: `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o bootstrap ./cmd/lambda`, then zip it.
- **arm64 (Graviton)** is ~20% cheaper per ms than x86 and usually as fast or faster.
- No Docker needed (Lambda *can* run container images up to 10 GB, but a zip of a static Go binary is smaller and cold-starts faster).

## Console test: invocation types
| | Synchronous (request/response) | Asynchronous (event) |
|---|---|---|
| Caller | waits for the result | gets "accepted" immediately |
| Errors | returned to caller | Lambda retries 2×, then failure destination (SQS/SNS) |
| Used by | **API Gateway**, function URLs, `aws lambda invoke` | S3 uploads, SNS, EventBridge |
- Third model: **poll-based** (SQS, Kinesis, DynamoDB Streams). Lambda polls and sends batches to your function.
- The browser editor can't open `bootstrap`: it's a binary over 3 MB. "✓ Lambda Deployed" = upload worked. Compiled-language code is never edited in the console.

## My real test results (2026-11-15)
| Run | Duration | Init | Billed | Memory | Note |
|---|---|---|---|---|---|
| `/health` #1 | 1.62 ms | **69.52 ms** | 72 ms | 26 MB | **cold start**: init is billed for custom runtimes |
| `/health` #2 | 15.90 ms | none | 16 ms | 26 MB | **warm**: no init |
| `/folders` | 403 ms | none | 404 ms | 38 MB | first DynamoDB call: DNS + TLS handshake; later calls ~10–30 ms (clients reused) |
- **Response JSON** (`statusCode`, `headers`, `body` as an escaped string) = the adapter's output. API Gateway turns it into a real HTTP response.
- `Access-Control-Allow-Origin: http://notes-app-web-cj...` came from our CORS middleware reading the `ALLOWED_ORIGIN` env var.
- **Why `/folders` → `200 []` proves the setup:** `/health` touches no AWS service; `/folders` runs a real DynamoDB Query. Success needs: (1) the SDK found the role's temp creds, (2) the trust policy let Lambda assume the role, (3) IAM allowed `dynamodb:Query` on `table/notes-app`, (4) the table exists in us-east-2. Any failure → 500 + an ERROR log (`AccessDeniedException` / `ResourceNotFoundException`). `[]` because the real table is new (local data lives in DynamoDB Local).
- 26 of 256 MB used: we could use 128 MB, but **CPU scales with memory**, so 256 MB gives faster cold starts at ~$0 difference for us.

## CloudWatch Logs: reading them
```
Log group   /aws/lambda/notes-app-api          ← one per function
 └ Log stream 2026/11/15/[$LATEST]826f21c1...  ← one per execution environment (instance)
    └ Log events                               ← every stdout/stderr line, captured automatically
```
- `INIT_START Runtime Version: provided:al2023...` = **cold start**; appears once per stream.
- Each invocation: `START` → our `slog` JSON line(s) → `END` → `REPORT` (Duration, Billed, Memory, Init Duration when cold).
- All 3 tests landed in **one stream** = the same warm instance was reused. Concurrent requests → extra instances → extra streams, each with its own `INIT_START`.
- **Two request IDs per invocation:** `RequestId: febc1f4b-...` on START/END/REPORT = Lambda's UUID (always new). `"requestId":"169.254.20.215/CMB5MV3kJh-000004"` inside our JSON line = chi's middleware: `<hostname>/<random prefix made at process start>-<in-memory counter>`.
- chi's requestId went `-000001` → `-000004` with the same prefix: an in-memory counter that survived between requests (same process). Warm `/folders` dropped 404 ms → 66 ms (connection reuse). That's why in-memory state "seems" to work on Lambda but breaks: other instances have their own memory → keep state in DynamoDB/S3.
- Our role's `logs:CreateLogGroup/CreateLogStream/PutLogEvents` on this group is what lets Lambda write here.
- **Retention defaults to "never expire"** (you pay for storage forever). Set it: log group → Actions → Edit retention → 1 week.
- **Interview line:** "Lambda ships stdout to CloudWatch Logs: one log group per function, one stream per execution environment. The REPORT line tells me duration, billed time, memory, and cold-start init time."

## Function vs instance vs invocation
| Term | Meaning | My 4 console tests |
|---|---|---|
| **Function** | what you create: `notes-app-api` (code + config) | 1 |
| **Instance** (execution environment) | a running copy in a micro-VM; handles **one request at a time** | **1** |
| **Invocation** | one request handled by an instance | **4** |
- `/health` and `/folders` are NOT separate Lambdas: one function holds the whole chi router; chi picks the handler by path inside the same process.
- Sequential requests reuse the free instance. A 2nd instance only starts if a request arrives while the first is busy.

## Warm reuse: pros and cons of in-memory state
- **Pros:** no cold start (72 → 16 ms), connection reuse (404 → 66 ms), safe caching of things identical for every user (AWS clients, config, lookup tables).
- **Cons:**
  - **Unreliable:** instances die anytime; parallel requests hit other instances with their own memory. A cart kept in a Go map "works" in testing, then vanishes in prod.
  - **Cross-user leaks:** per-request data in a global variable that isn't reset can be seen by the *next* request (maybe another user). This is a real security bug class.
  - **Leaks accumulate:** memory leaks and open files survive across invocations until the instance runs out of memory.
- **Rule:** per-user/per-request data → local variables or DynamoDB/S3. Shared read-only setup → globals (that's what makes warm starts fast).
- The chi request-ID counter was just *visible evidence* of reuse. It's harmless (the random per-process prefix keeps IDs unique).
- **Interview line:** "Lambda reuses execution environments, so I initialize clients globally for speed, but I never keep request or user state in memory: it isn't shared across instances and can leak between invocations."

## Config differences
- No `DYNAMODB_ENDPOINT` / `S3_ENDPOINT` set → SDK uses the real AWS endpoints.
- Credentials: Lambda injects the execution role's temporary creds as env vars; the SDK's default provider chain picks them up. No keys in code or config.
- Env vars we set on the function: `TABLE_NAME`, `PHOTO_BUCKET`, `ALLOWED_ORIGIN`. `AWS_REGION` is set by Lambda automatically.
- Init outside the handler: AWS clients are built once in `main()` and reused across warm invocations (connection reuse = faster).

## Limits worth knowing
- Max run time 15 min (API Gateway HTTP API times out at 30 s anyway).
- Request/response payload ~6 MB → why photos go straight to S3 via presigned URLs.
- Memory 128 MB–10 GB; CPU scales with memory.
- `/tmp` is ephemeral (512 MB default); never store state there.

## Interview one-liners
- "API Gateway and the Lambda service are always on; only my function cold-starts. I pay per request, nothing when idle."
- "My handlers are plain `net/http`. On Lambda an adapter converts the API Gateway event into an `http.Request`, so the same chi router runs locally, in Docker, and on Lambda. Only `main()` differs."
- "Go on Lambda = `provided.al2023` + a static binary named `bootstrap`, built for arm64 because Graviton is cheaper."
- "Clients are initialized in `main()`, outside the handler, so warm invocations reuse connections."
- "Big payloads don't go through Lambda (6 MB limit); clients upload straight to S3 with presigned URLs."
