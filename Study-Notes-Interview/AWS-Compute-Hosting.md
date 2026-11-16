# Where does each piece live? Local → AWS, and compute options

## Local → AWS mapping
| Piece | Local (Phases 1–4) | AWS (Phase 5) |
|---|---|---|
| Frontend | `python3 -m http.server 5500` on the Mac | **S3 static website hosting** (bucket `notes-app-web-cj`) |
| Backend | Go API in a Docker container (compose) | **Lambda** (our Go binary) behind **API Gateway** (public HTTPS URL) |
| Database | DynamoDB Local container (`amazon/dynamodb-local`) | **DynamoDB** table `notes-app` |
| Photos | LocalStack container (fake S3) | **S3** bucket `notes-app-photos-cj` (private) |
| Logs | `docker compose logs api` | **CloudWatch Logs** |
| Credentials | dummy `test/test` | **IAM role** assumed by Lambda (temporary creds, no keys in code) |

Same Go code. Only env vars change (no `DYNAMODB_ENDPOINT`/`S3_ENDPOINT` → the SDK uses the real AWS endpoints).

```
Browser ──► S3 website (index.html)
   │
   ├──fetch──► API Gateway (HTTPS) ──► Lambda (Go, IAM role) ──► DynamoDB
   │                                          └──► S3 (sign URLs, HeadObject, delete)
   └──presigned PUT/GET──► S3 photo bucket
```

## The compute menu: "where does my code run?"
From "I manage everything" to "AWS manages everything":
| Service | What you hand AWS | Who manages servers | Scaling | Idle cost | Good for |
|---|---|---|---|---|---|
| **EC2** | A virtual machine (you install everything) | **You** (OS patches, restarts) | You configure Auto Scaling Groups | Pay 24/7 | Full control, legacy apps |
| **ECS on EC2** | A container image | You manage the EC2 hosts, ECS places containers | ECS service + ASG | Pay 24/7 | Containers with custom hosts |
| **ECS on Fargate** | A container image | **AWS** (serverless containers) | ECS adds or removes tasks (CPU/req metrics) | Pay while tasks run (≥1 task = 24/7) | Long-running containerized APIs |
| **EKS** | Kubernetes manifests + images | AWS runs the K8s control plane; you run nodes (or Fargate) | HPA / Karpenter | Control plane ~$73/mo + nodes | Teams standardized on Kubernetes |
| **App Runner** | Image or source | AWS | Automatic | Low | Simple web apps (*denied by SCP in this account*) |
| **Lambda** | A zip (binary) **or** a container image | **AWS** | **Automatic, per request**: 0 → thousands of concurrent copies | **$0 when idle** | Spiky/low traffic APIs, event handlers |

- **ECR (Elastic Container Registry)** isn't compute. It's **Docker Hub inside AWS**: a private place to `docker push` images so ECS, EKS or Lambda can pull them.
- **API Gateway** isn't compute either. It's the **front door**: a public HTTPS URL, routing, CORS, throttling, auth. It calls Lambda.

## Why Lambda for this project
- **Cost:** a demo with almost no traffic → Lambda + API Gateway + DynamoDB on-demand ≈ **$0** (free tier). Fargate would bill 24/7 for at least one task, and EKS ~$73/mo just for the control plane.
- **No servers to manage**, and scaling is automatic.
- **The same chi router runs unchanged:** an adapter (`aws-lambda-go-api-proxy`) converts API Gateway events into normal `http.Request`s.
- Tradeoffs: **cold starts** (first request after idle spins up a new copy, ~100 ms for Go), 15-min max runtime, ~6 MB request/response payload (fine: photos bypass it via presigned URLs), and you don't control the machine.

## How it scales: containers vs Lambda
- **Our Docker image (Phases 3–4):** one container = one process. Handling more load = **run more copies** (ECS tasks or K8s pods) behind a **load balancer**, scaled on CPU or request count. Copies stay running even at 0 traffic.
- **Lambda:** each concurrent request gets its own **execution environment** (a micro-VM, Firecracker). 100 simultaneous requests → up to 100 copies, automatically. After idle, copies are frozen and then discarded → $0. Concurrency limit is per account (default 1000 per region, adjustable).
- **Both work only because the API is stateless:** all state is in DynamoDB and S3, so any copy can serve any request.
- **DynamoDB on-demand and S3** scale automatically too. Nothing to provision.

## What happens to our Dockerfile?
- Still used for **local dev** (compose) and the **stretch goals**: push to **ECR**, run on **ECS Fargate**, or deploy to **Kubernetes** (kind locally).
- Lambda gets a **zip** with a binary named `bootstrap` (runtime `provided.al2023`, arm64/Graviton = cheaper and faster). Lambda can also run container images from ECR, but a zip is simpler for Go.
- Same code, two packagings: **zip → Lambda**, **image → ECS/EKS**. A good interview point: "I kept the HTTP router transport-agnostic, so it runs as a server or a Lambda."

## Interview one-liners
- "EC2 = VMs I manage; ECS/Fargate = containers AWS runs; EKS = managed Kubernetes; Lambda = functions, scale to zero; ECR = the image registry."
- "I chose Lambda + API Gateway because traffic is spiky/low and it scales to zero. For steady high traffic or long-lived connections, I'd move the same container to ECS Fargate."
- "It's stateless, so it scales horizontally: more Lambda environments or more tasks, same DynamoDB table."

---
## Q&A (before Phase 5.2)

### Q1. Is Lambda more expensive per request?
- **Yes, per request it's pricier than a running container, but you pay nothing between requests.** Rough us-east-2 prices (check the pricing pages; they change):
  - Lambda: ~$0.20 per 1M requests + compute (GB-seconds). A 128 MB, 50 ms Go call ≈ **$0.28 per 1M requests**.
  - API Gateway HTTP API: ~**$1.00 per 1M requests**.
  - → ~**$1.30 per million API calls**. Our demo: maybe 1,000 calls → fractions of a cent (and Lambda's always-free tier covers 1M requests/month).
- Fargate equivalent: the smallest task (0.25 vCPU, 0.5 GB) running 24/7 ≈ **$7–9/month** + an **Application Load Balancer** to reach it ≈ **$16+/month** → ~$25/month even at zero traffic.
- **Break-even:** roughly tens of millions of requests/month (several requests/second, nonstop). Below that, Lambda wins. Above it, containers win.
- **DynamoDB and S3 reads/writes cost the same no matter who calls them** (Lambda or ECS). Compute choice doesn't change DB cost.

### Q2. The moving parts of "one backend" and why each exists
**Lambda isn't a web server.** It has no URL and no port. It just runs a function when **invoked with an event**. Something must accept HTTPS from the internet and turn it into an invocation → **API Gateway**.

| Part | Job | Restaurant analogy |
|---|---|---|
| **API Gateway** (HTTP API) | Public HTTPS URL + TLS cert, routes requests, CORS, throttling, auth (JWT), converts HTTP → event → invokes Lambda | Front desk: greets guests, checks reservations, passes orders to the kitchen |
| **Lambda** | Runs our Go code per request, scales copies automatically | Chef called in per order |
| **IAM role** | The Lambda's identity and permissions (only our table, only our bucket) | Staff badge: which rooms the chef may enter |
| **DynamoDB** | Data | Pantry |
| **S3** | Photos (and the frontend files) | Warehouse |
| **CloudWatch Logs** | Every log line from Lambda, plus metrics and alarms | Security cameras + log book |

So the **backend = API Gateway + Lambda + IAM role** (+ CloudWatch for visibility), using DynamoDB and S3.
- Alternatives to API Gateway in front of Lambda: **Lambda Function URL** (simplest, a direct HTTPS URL, fewer features) or an **ALB**.

**What is Fargate?** It's **not** a front door. It's a **way to run ECS (or EKS) containers without managing servers**. ECS has two "launch types": **EC2** (your own VMs host the containers) or **Fargate** (AWS provides the hosts).
| Lambda world | Container world |
|---|---|
| **Lambda** (runs code) | **ECS service on Fargate** (runs containers) |
| **API Gateway** (front door) | **Application Load Balancer** (front door, spreads traffic over tasks) |
| zip / `bootstrap` binary | Docker image in **ECR** |

### Q3. What "stateless" means (it's not about GET vs POST)
- GET and POST do work the same way. **"Stateless" is about the server's memory between requests**: does the server *remember* anything locally that the next request needs?
- **Stateless (ours):** each request reads and writes DynamoDB and S3. No sessions, caches or files kept in the process. Any copy can serve any request.
- **Stateful examples that break with Lambda (and with multiple container copies):**
  - Login sessions kept in an in-memory map → the next request lands on another copy that doesn't know you.
  - An in-memory counter (`visits++`) → each copy has its own count, which is lost on shutdown.
  - Saving uploads to local disk → other copies can't see them. Lambda's `/tmp` is wiped when the environment dies.
  - WebSocket chat or game servers holding open connections and live match state.
- Fix: push state out to a shared store (DynamoDB, ElastiCache/Redis, S3). Lambda **forces** this (environments vanish). Containers **should** do it too, to scale horizontally.

**When to use what:**
| Use | When | Examples |
|---|---|---|
| **Lambda** | Spiky/low traffic, event-driven, short tasks (<15 min) | This API, webhooks, "S3 upload → make thumbnail", processing SQS messages, nightly cron jobs |
| **ECS Fargate** | Steady traffic, long-running processes, persistent connections, no cold starts allowed | A busy REST API, WebSocket chat, a queue worker running all day, a containerized app you already have |
| **EKS** | Many microservices, a platform team, need the Kubernetes ecosystem or portability across clouds | A large company's 50-service platform |
| **EC2** | Full OS control, special software/licensing, GPUs for custom stacks, self-hosted databases | Legacy apps, ML training boxes |

### Q4. No Docker for Lambda? Then how does it run our code?
1. We cross-compile Go for Lambda's machine: `GOOS=linux GOARCH=arm64 go build -o bootstrap ./cmd/lambda`, then zip `bootstrap` → upload.
2. Runtime **`provided.al2023`** = an Amazon Linux 2023 environment **that AWS provides and patches**. It plays the role our distroless base image played. The name `bootstrap` is the file Lambda looks for and executes.
3. On a request: AWS starts a **Firecracker micro-VM** (similar isolation to a container, managed by AWS) → unzips our code → runs `./bootstrap` (**cold start**, ~100 ms).
4. Our `main()` calls `lambda.Start(...)`, which asks Lambda's internal **Runtime API** "next event?" → gets the API Gateway event → the **adapter** turns it into an `http.Request` → **the same chi router** handles it → the response goes back to API Gateway → the browser.
5. The environment is **frozen and reused** for the next requests (**warm**: fast). After idle, it's discarded.
- Dockerfile ↔ Lambda: stage 1 (`go build`) = our zip build step. Stage 2 (distroless base) = AWS's `provided.al2023`.
- Lambda **can** also run a Docker image (pushed to ECR, up to 10 GB). For a single Go binary, a zip is simpler and starts faster.

### Q5. The request flow, corrected
- **The browser always starts it.** Lambda never contacts the frontend. It only answers when invoked.
- **The unzip happens only on a cold start.** Warm requests reuse the already-running environment.
- **Firecracker is the micro-VM technology. Lambda is the service** that uses it.
```
1. Browser (JS from index.html) ── fetch ──► API Gateway
2. API Gateway ── invoke(event) ──► Lambda
      cold start only: new Firecracker micro-VM → unzip → run ./bootstrap
3. Lambda (our Go code) ──► DynamoDB / S3 as needed
4. Lambda ── response ──► API Gateway ──► browser
```

### Q6. How can S3 "host" the frontend? Does it run anything?
- **No. S3 runs nothing.** It's a file server: ask for a file, get the file.
- **Our frontend is "static":** one `index.html` (HTML + CSS + JS). The server just hands over the file. **The JavaScript runs in the user's browser**, not on any AWS server.
- **Static vs dynamic:**
  - *Static* = the same file for everyone, no server code per request → S3 can serve it.
  - *Dynamic* = needs code per request (query DB, check permissions) → needs compute (Lambda/ECS).
- **S3 static website hosting** gives a bucket its own HTTP endpoint: `http://notes-app-web-cj.s3-website.us-east-2.amazonaws.com`. Visiting it = S3 returns `index.html`. **No API Gateway involved**, because S3 answers web requests itself.
- **Two separate hosts, one page:**
```
1. Browser ── GET / ──► S3 website endpoint ──► returns index.html (once)
2. Browser runs the JS from that file
3. JS ── fetch ──► API Gateway ──► Lambda ──► DynamoDB/S3   (every API call)
4. JS ── presigned PUT/GET ──► S3 photo bucket              (photos)
```
- Same as local: `python3 -m http.server 5500` just served files (= S3 website), and the API was separate on 8080 (= API Gateway + Lambda).
- Tradeoffs: the S3 website endpoint is **HTTP only** and must be **public**. Prod upgrade: **CloudFront** (CDN) in front → HTTPS, custom domain, edge caching, and the bucket stays private (Origin Access Control).

### Q7. Static vs dynamic, using a healthcare booking app as the example
**The test: is the response a file that already exists, or does code have to compute it for this request?**
- **Static** = a file that already exists, identical for everyone → just hand it over (S3/CDN).
- **Dynamic** = the answer depends on *who's asking*, *when*, or *what's in the database right now* → code must run (Lambda/ECS/EC2).

**The confusing part:** a Vue/React app *feels* dynamic (pages change, buttons, forms), but that's **JavaScript running in the browser**. `npm run build` produces **static files** (`dist/index.html`, `app.js`, `app.css`). The server never runs Vue; it ships the files once. **Dynamic UI ≠ dynamic server.**

**Hospital / doctor booking / e-pharmacy split:**
| Feature | Static or dynamic | Why |
|---|---|---|
| The Vue app itself (pages, forms, CSS, logo) | **Static** | Same files for every user |
| "Dr. Rao's free slots on Tuesday" | **Dynamic** | Depends on the DB right now |
| Book slot 10:30 | **Dynamic** | Must check it's still free and save it atomically (no double booking) |
| Login / "my appointments" | **Dynamic** | Depends on who you are (auth) |
| Pharmacy stock, cart total, discounts | **Dynamic** | Inventory and prices change; totals are computed per order |
| Payment | **Dynamic** | Calls the payment gateway with a secret key (must never live in browser JS) |
| Upload prescription / lab report | **Dynamic** URL + **static** storage | API checks permissions and signs a URL; file goes to S3 (like our photos) |
| SMS/email reminder 1 hour before | **Dynamic** (scheduled) | A cron job queries upcoming appointments and sends messages |

**Rule:** anything involving **secrets, permissions, shared data, or "the latest state"** must be dynamic (server-side). The browser can't be trusted, and it can't see other users' data.

**Likely AWS setup for such a startup** (typical, not confirmed):
```
Vue build (static) ──► S3 + CloudFront (HTTPS, CDN)
Browser JS ──► API Gateway ──► Lambda (Node.js handlers)  ──► RDS Postgres/MySQL or DynamoDB
                                     ├─► S3 (reports, prescriptions via presigned URLs)
                                     ├─► Cognito (login) / SES (email) / SNS (SMS)
EventBridge schedule ──► Lambda (send reminders)
```

### Q8. Why keep ECS containers running 24/7? Can't they be stateless too?
- **Yes, they should be stateless too.** Same rule: state goes to DynamoDB/RDS/S3/Redis. Containers aren't kept alive to *hold variables*.
- They're kept running because of **the process**, not the data:
  1. **No cold starts:** always warm, predictable latency (useful for heavy runtimes like Java Spring Boot, where a cold start can take seconds).
  2. **Long-lived connections:** WebSockets (live chat, a doctor–patient video signaling server), streaming, gRPC streams. A connection lives in one process for minutes or hours. Lambda's model is short request → response.
  3. **Long jobs:** anything over Lambda's **15-minute** limit (big report generation, video processing, data migrations).
  4. **Always-on workers:** a process continuously consuming a queue or Kafka stream.
  5. **Database connection pools:** a few containers hold a steady pool of, say, 20 connections to Postgres. 1,000 concurrent Lambdas can open 1,000 connections and overwhelm RDS (fix: **RDS Proxy**).
  6. **Cost at steady high traffic:** a busy, always-used server is cheaper than paying per request.
  7. **Warm in-memory caches** as an *optimization* (not the source of truth): if lost, just refill from the DB.
- **"Stateful" in the bad sense** = the *only* copy of important data lives in process memory or local disk. Avoid that everywhere, on Lambda, ECS or EC2.

### Q9. Static/dynamic vs stateless/stateful: two different questions
| Question | Axis | Dr. Rao's free slots |
|---|---|---|
| Is the response a pre-made file, or computed from current data at request time? | **Static vs dynamic** (about the *response*) | **Dynamic**: computed from DynamoDB *now* |
| Does the server process remember anything between requests? | **Stateless vs stateful** (about the *server*) | **Stateless**: it forgets everything and re-reads DynamoDB every time |
- So "Dr. Rao's slots" = **dynamic response from a stateless server**. That's the normal, healthy combo.
- "Dynamic" doesn't mean "different per user." It means "can't be a pre-built file, because it depends on changing data."
  - **Shared dynamic:** Dr. Rao's slots are the same for everyone *at this moment*, but they change the instant someone books. You can cache it briefly (seconds) as a middle ground.
  - **Per-user dynamic:** "my appointments" differs per person and must never be shared-cached.
  - **Static:** the logo changes only when you redeploy.
- **Should every app be stateless?** The **app servers** (Lambda/ECS/EC2 running your code): yes, as the default for small and big apps alike. State is pushed down into **dedicated stateful services** (DynamoDB, RDS, S3, Redis). Those *are* stateful; that's their job, and AWS handles their durability and replication. Exceptions are things where a live *connection* is the state (WebSocket servers, game servers).

### Q10. "1000 Lambdas → 1000 DB connections": what that means
- **No copies of the database are made.** There's **one** database. Each Lambda opens a **connection** (a network line) to it.
- **Postgres/MySQL (RDS):** each connection is a long-lived TCP session that costs memory on the DB server, and there's a max (often a few hundred). 1000 Lambdas → 1000 connections → "too many connections" errors. Fix: **RDS Proxy** (pools and shares connections).
- **DynamoDB is different:** every call is a short **HTTPS request** (no persistent connection), and it's built for massive parallelism. 1000 Lambdas → fine. That's why **Lambda + DynamoDB** is a classic pair (and why your startup's choice made sense).
- The DB doesn't process requests strictly one by one. It handles many **in parallel**. Correctness when two requests touch the same data is handled by **conditional writes and transactions** (see DynamoDB.md → double booking).

## Tags: why we tag everything (`project=notes-app`)
Tags are key=value labels on resources. AWS ignores them; *you* use them to slice the account.
| Use | Example |
|---|---|
| Cost tracking | Cost Explorer → group by tag `project` (activate it as a cost allocation tag in Billing) |
| Find everything | Resource Groups → **Tag Editor** → `project=notes-app` lists tables, buckets, Lambdas, roles, alarms across services |
| Safe teardown | scripts delete only resources carrying the project tag |
| Access control (ABAC) | IAM: "may only modify resources tagged `team=payments`" |
| Automation | "stop everything tagged `env=dev` at 7 p.m." |
- Companies often **enforce** tags (org Tag Policies / SCPs reject untagged resources: `owner`, `env`, `cost-center`).
- **Interview line:** "Every resource is tagged with project/env/owner so cost, ownership and cleanup are queryable, not tribal knowledge."
