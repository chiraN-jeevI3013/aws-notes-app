# Docker

## Core ideas
- **Image:** a read-only template (OS files + app + config). Example: `amazon/dynamodb-local`.
- **Container:** a running instance of an image. It's isolated, but shares the Mac's (Linux VM's) kernel. It's not a full VM.
- **Port mapping `8000:8000`:** `host:container`. Traffic to `localhost:8000` on the Mac is forwarded into the container's port 8000.
- **docker-compose.yml:** declares several containers (services) in one file, so `docker compose up` starts them all with one command.
- **Docker Desktop:** the Mac app that runs a small Linux VM where containers actually run (containers need a Linux kernel).

## Naming: project vs container vs image
- **Compose project** `cj-fun-aws-notepad-app`: the group. Compose names it after the folder that holds `docker-compose.yml`.
- **Container** `cj-fun-aws-notepad-app-dynamodb-1`: the name is `<project>-<service>-<replica#>`. `dynamodb` is the service name from the YAML. `1` means it's the first copy (you could scale to more).
- **Image** `amazon/dynamodb-local:latest`: the image name is `<publisher>/<repo>:<tag>`, pulled from Docker Hub. The tag `latest` is just a label. In real projects, pin a version so builds are repeatable.

## Command cheat sheet: when to use what
| I want to... | Command |
|---|---|
| Check Docker is running | `docker info` (errors if Docker Desktop is closed) |
| Start everything in docker-compose.yml in the background | `docker compose up -d` |
| See running containers | `docker ps` (`docker ps -a` includes stopped ones) |
| See a container's logs | `docker compose logs -f dynamodb` (Ctrl+C to stop following) |
| Stop and remove the compose containers | `docker compose down` |
| Restart one service | `docker compose restart dynamodb` |
| Get a shell inside a container | `docker exec -it <name> sh` |
| List downloaded images | `docker images` |
| Build an image from a Dockerfile (Phase 3) | `docker build -t notes-api .` |
| Clean up unused stuff | `docker system prune` (asks first) |

## Docker Desktop clicks
- **Containers** tab: lists `cj-fun-aws-notepad-app` (the compose project). Expand it to see `dynamodb-1`. Click it to see **Logs**, **Inspect** (env, ports), **Exec** (shell), and **Stats** (CPU and memory).
- The ▶ / ■ buttons start and stop the container. The trash icon removes it.
- **Images** tab: `amazon/dynamodb-local` was downloaded once and is reused.

## Healthchecks and debugging a container that "disappeared"
- **Healthcheck:** a command Docker runs every few seconds inside the container to ask "are you OK?". `docker ps` shows `(health: starting)` → `(healthy)`. Kubernetes uses the same idea with **liveness/readiness probes**.
- **The container vanished from `docker ps`?** It exited. The fix loop:
  1. `docker ps -a`: list exited containers too
  2. `docker compose logs <service>`: read why it died
  3. Fix it (env var, port, config), then `docker compose up -d` again
- **`.env` file:** Compose auto-reads `.env` next to `docker-compose.yml` and substitutes `${VAR}` in the YAML. Use it for secrets (like the LocalStack token) and **never commit it**.
- `docker compose up -d` is **idempotent**: running containers stay as they are (`Running`), and only new or changed services start.

## In this project
- Phase 1: only DynamoDB Local runs in Docker. It uses `-inMemory`, so **data is wiped when the container stops**.
- Phase 2: added **LocalStack** (fake AWS, S3 only) on port 4566. See [LocalStack.md](LocalStack.md).
- Phase 3: the Go API also gets its own container (multi-stage Dockerfile).

## Phase 3: containerizing the Go API
### Multi-stage Dockerfile
| Stage | Base image | Purpose | Size |
|---|---|---|---|
| `build` | `golang:1.27.4` | compile the Go code | ~800 MB, thrown away |
| final | `gcr.io/distroless/static-debian12:nonroot` | run only the binary | **23.3 MB** total |
- `COPY go.mod go.sum` → `go mod download` → *then* `COPY . .`: this ordering uses the **layer cache**. Changing code doesn't redownload dependencies.
- `CGO_ENABLED=0`: a static binary with no C library dependency, so it runs on a near-empty image.
- **Distroless** = no shell, no package manager. `docker exec <c> id` → `executable file not found`. Attackers get nothing to work with. The tradeoff: you can't shell in to debug.
- `:nonroot` / `USER nonroot` runs as UID 65532. If the app is compromised, the attacker isn't root.
- `.dockerignore` keeps `.env` (secrets) and docs out of the build context and the image.

### Container networking (the big Phase 3 idea)
- **Inside a container, `localhost` = that container itself**, not your Mac.
- Compose creates a network (`cj-fun-aws-notepad-app_default`) with built-in **DNS**: containers reach each other by **service name**, like `http://dynamodb:8000` and `http://localstack:4566`.
- Your Mac reaches containers through **published ports** (`8080:8080`), via `localhost`.
- **Presigned URL gotcha:** the API talks to S3 at `localstack:4566`, but your browser can't resolve `localstack`. The host is part of the signature, so the API signs URLs with a second client pointed at `S3_PUBLIC_ENDPOINT=http://localhost:4566`.

### Compose startup order
- `depends_on` + `condition: service_healthy` waits for LocalStack's healthcheck.
- `restart: on-failure`: if DynamoDB Local isn't accepting connections yet, the API exits and Docker restarts it. "Crash and let the orchestrator restart it" is normal in containers (Kubernetes does the same).

### Q&A from Phase 3
**How did 800 MB become 23 MB? Is it compressed?**
No. The final image simply **doesn't contain stage 1**. Stage 1 (golang, Go compiler + Debian + tools) is a temporary workshop. `COPY --from=build` takes *only* the compiled binary out of it, and the workshop isn't shipped. `docker history` on our image shows:
```
12.5MB  COPY /out/server /server     ← our Go binary (-ldflags="-s -w" strips debug info)
319kB   cacerts                       ← CA certificates, needed for HTTPS to AWS
~100kB  passwd, group, tmp, home...   ← minimal Linux files (distroless)
```
- `docker images` columns: `cj-fun-aws-notepad-app-api:latest  0d5bf48f958c  23.3MB  5.38MB  U` = **name:tag**, **image ID**, **disk usage** (unpacked on your Mac, plus Docker's own bookkeeping), **content size** (the **compressed** size, what you'd actually push or pull from a registry like ECR), and `U` = **in use** by a container.
- The image name `cj-fun-aws-notepad-app-api` = `<compose project>-<service>`, auto-named by Compose.

**What is layer caching?**
- Each Dockerfile instruction (`FROM`, `COPY`, `RUN`) makes a **layer**, a saved snapshot. Docker reuses a layer if the instruction **and its inputs** haven't changed since last time. Once one layer changes, every layer **after** it is rebuilt.
- So order matters: put slow, rarely changing steps first.
```
WORKDIR /src                 CACHED  (unchanged)
COPY go.mod go.sum ./        CACHED  (deps unchanged)
RUN go mod download          CACHED  ← the slow network step, skipped
COPY . .                     rebuilt (router.go changed: "ok" → "ok-docker")
RUN go build ...             rebuilt (its input changed)
COPY --from=build /server    rebuilt
```
**Reading the CACHED lines: my two builds side by side**

Each Dockerfile line produces a saved snapshot called a **layer**. When you build again, Docker checks: *"Did this line or its input files change?"*
- **No:** it reuses the saved layer, and you see `CACHED`.
- **Yes:** it rebuilds that layer **and every layer after it**.

| Step | 1st build | 2nd build (after `ok-docker`) |
|---|---|---|
| 3/6 `COPY go.mod go.sum` | CACHED | CACHED |
| 4/6 `RUN go mod download` | CACHED | **CACHED** ← the slow download was skipped |
| 5/6 `COPY . .` | CACHED | **rebuilt**: router.go changed |
| 6/6 `RUN go build` | CACHED | **rebuilt**: 5.0 s |
| `COPY --from=build` | CACHED | **rebuilt** |
| Total | 1.1 s | 5.9 s |

My first build was entirely CACHED because Claude had built the same code earlier while verifying, and Docker kept those layers. In the second build, `go mod download` stayed CACHED even though code changed. That's the payoff of copying `go.mod` *before* the code. If the Dockerfile copied all the code first, every small edit would re-download every dependency.

- My two real builds: **1st** run = *everything* CACHED (1.1 s), because I had already built the same code while verifying, and Docker kept the cache. **2nd** run (after editing `/health`) = steps 5/6, 6/6 and the final copy rebuilt (5.9 s). The other steps stayed CACHED.
- If we had done `COPY . .` *before* `go mod download`, then **any** code edit would redownload every dependency.

**What are distroless and nonroot?**
- A normal base image (`ubuntu`, `debian`) is a small Linux distribution: `bash`, `ls`, `apt`, `curl`, and so on. **Distroless** (by Google) removes all of that. It holds only what a static binary needs: CA certs, timezone data, `/etc/passwd`, and `/tmp`.
- **"No shell or tools"** means `docker exec -it api-1 sh` fails, because there's no `sh` to run. If an attacker finds a bug in our app, they can't open a shell, `curl` out, or `apt install` malware. That's a smaller **attack surface**, and fewer packages means fewer CVEs for security scanners to flag.
  - *Tradeoff:* you can't shell in to debug. Use logs, or a debug variant of the image (`:debug` tags include busybox).
- **nonroot:** the process runs as UID **65532** instead of **root (UID 0)**. A compromised process has no admin rights inside the container, which makes it much harder to escape to the host. Kubernetes can enforce this with `runAsNonRoot: true`.

**What changed about ports when the backend moved into Docker?**
- From the Mac's point of view, **nothing**. It's still `localhost:8080`, so curl, the smoke tests, and (later) the browser didn't change at all.
- What changed is *who listens*: before, `go run` on the Mac listened on 8080. Now Docker listens on the Mac's 8080 and forwards traffic into the `api` container's 8080 (the `"8080:8080"` port mapping).
- You can't run both: if `go run` is still on 8080, compose fails with "port is already allocated."

**The compose `api` service, explained**
- **`build: .`**: build the image from the Dockerfile in this folder, instead of pulling a ready-made one (`image:`).
- **`ports: "8080:8080"`**: **Mac port : container port**. It's the door from your Mac into the container.
- **Inside a container, `localhost` means the container itself.** Each container is its own little machine with its own `localhost`. If the API used `http://localhost:8000`, it would knock on *its own* door, where nothing listens.
- **Compose network and DNS:** Compose puts all 3 containers on one private network and gives each a hostname equal to its **service name**. So the API uses `http://dynamodb:8000` and `http://localstack:4566`. It's like apartments in a building: neighbors are reached by apartment name, and visitors (your Mac) come in through the front door (published ports).
```
 Your Mac ── localhost:8080 ──► [api]  ──► dynamodb:8000   [dynamodb]
          ── localhost:4566 ──► [localstack] ◄── localstack:4566 ── [api]
```
- **Why `S3_PUBLIC_ENDPOINT`:** the API reaches S3 as `localstack:4566`, but the presigned URL is used by **your Mac** (curl or browser), which doesn't know the name `localstack`. It only knows `localhost:4566`. The hostname is part of the signature, so the API signs URLs for `localhost:4566` using a second S3 client.
**How does a photo actually flow in Docker? (who talks to whom)**
- The DB **never stores URLs**, only keys (`notes/<noteId>/<uuid>.jpg`). The API **creates** a presigned URL on the spot: it does local math (an HMAC signature) using its credentials, with **no network call**. It's like writing and signing a permission slip.
- The API **hands the slip to the Mac**, and **the Mac** uses it to go to S3 directly. The photo bytes never go through the API.
- So the hostname written *in the URL* must be one the **Mac** can reach: `localhost:4566`. The API itself never uses `localhost:4566`; it uses `localstack:4566`.

| # | Who → whom | Address used | What |
|---|---|---|---|
| 1 | Mac → API | `localhost:8080` | "give me an upload URL" |
| 2 | API (no network) | n/a | signs a URL **for host `localhost:4566`** |
| 3 | Mac → LocalStack | `localhost:4566` (from the URL) | PUT the photo bytes |
| 4 | Mac → API | `localhost:8080` | "done, here's the key" |
| 5 | API → LocalStack | `localstack:4566` (inside the network) | HeadObject: is it really there? |
| 6 | API → DynamoDB | `dynamodb:8000` | save the key on the note |
| 7 | Mac → API | `localhost:8080` | list notes → API signs fresh GET URLs (no network) |
| 8 | Mac → LocalStack | `localhost:4566` (from the URL) | GET the photo |
- On real AWS this problem disappears: the API (Lambda) and the browser both use the same public hostname, `notes-app-photos.s3.us-east-2.amazonaws.com`, so one S3 client is enough.

- **`depends_on` + `service_healthy`**: don't start the API until LocalStack's healthcheck passes.
- **`restart: on-failure`**: if the API crashes at startup (say DynamoDB isn't ready yet), Docker restarts it automatically.

### Commands
```bash
docker compose up -d --build     # build the API image (if code changed) and start everything
docker compose logs -f api       # follow the API's JSON logs
docker compose ps                # status of the 3 services
docker compose build api         # rebuild just the image
docker images | grep notepad     # see the built image and its size
docker compose down              # stop and remove all 3 containers (data is wiped: in-memory)
```

## Interview one-liners
- "Containers package the app and its dependencies, so it runs the same on my laptop, in CI, and in prod."
- "Image = class, container = object."
- "Multi-stage builds compile in a big Go image, then copy just the binary into a tiny runtime image. That makes it smaller, faster to pull, and gives it less attack surface."
