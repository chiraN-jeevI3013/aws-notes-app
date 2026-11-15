# Go (as used in this project)

## net/http basics
- **`http.Handler`** is an interface with one method: `ServeHTTP(w http.ResponseWriter, r *http.Request)`. Everything (routers, middleware, handlers) speaks this interface. That's why pieces plug together.
- **`http.ListenAndServe(":8080", h)`** opens a TCP port and calls `h.ServeHTTP` for every request, each one in its own goroutine.
- **Middleware** = a function `func(next http.Handler) http.Handler`. It wraps the next handler to do work before or after it (logging, CORS, recovering from panics).

## chi
- A small router built on top of net/http. It matches **method + path pattern** (`DELETE /folders/{folderId}`), reads path params with `chi.URLParam(r, "folderId")`, and groups routes with `r.Route("/folders", ...)`.
- Why chi rather than a big framework (Gin/Echo): it's 100% `http.Handler`-compatible, so the same router runs locally and on Lambda via an adapter. It also gives us the **route pattern** for logs (`/folders/{folderId}`, not `/folders/8f3a...`).
- Note: since Go 1.22 the standard `http.ServeMux` supports methods and `{wildcards}` too. chi adds route groups, a middleware chain, and route patterns.

## Ports in this project
| Port | Who listens | Who calls it |
|------|-------------|--------------|
| 8080 | Our Go API | curl / smoke.sh / browser (later) |
| 8000 | DynamoDB Local (container) | Only our Go API, via the AWS SDK |
- **Two copies of the server (8080 + 9090) = two separate processes.** They share nothing in memory, but both talk to the same DB on 8000. The DB accepts many client connections, so they don't clash, and they see the same data because the server is **stateless** (all state lives in the DB). That's exactly how you scale horizontally: N Lambdas or N containers behind one database.
- One process per port. The client never talks to the DB directly. Same idea in AWS: browser → API Gateway → Lambda → DynamoDB.

## Q: Is the Go server running on 2 ports now?
I ran `go run ./cmd/server` (port 8080) and then `PORT=9090 ... go run ./cmd/server`. `lsof` showed:
```
com.docker  → port 8000   (DynamoDB Local container)
server      → port 8080   (first go run)
server      → port 9090   (second go run)
```
- **Each `go run` is a separate process**, and each process binds one port. So yes, there were two copies of the backend.
- **Why we need a backend:** clients (browser, curl) talk only to the Go API. Only the API talks to the DB. The CLI (`aws dynamodb scan --endpoint-url ...`) is a developer side door, not something users get.
- **Two backends, one DB, no clash:**
```
curl → :8080 (Go copy A) ─┐
                          ├──> :8000 DynamoDB Local (one database)
curl → :9090 (Go copy B) ─┘
```
  - A database accepts many concurrent connections. Each Go copy opens its own.
  - Both copies see the same data: a folder created via 9090 shows up when you list via 8080.
  - It works because the server is **stateless**: it keeps nothing in memory, and all state is in the DB.
- **Real-world version:** under load, Lambda runs many copies of our Go code at once, all hitting one DynamoDB table. Kubernetes does the same with N pod replicas behind a Service.
- **Interview line:** "The API is stateless, so it scales horizontally: add instances, same database."
- Stop an extra copy with Ctrl+C. Two processes can't bind the same port ("address already in use").

## Config via environment variables (12-factor style)
- `main.go` reads `PORT`, `TABLE_NAME`, `DYNAMODB_ENDPOINT`, `ALLOWED_ORIGIN`, `AWS_REGION`, with defaults.
- The same binary behaves differently locally vs on AWS just by changing env vars. No code change, no rebuild.

## Interview one-liners
- "Everything in Go's HTTP stack is an `http.Handler`, so middleware is just a function that wraps a handler."
- "I keep config in env vars, so one build artifact is promoted across environments."
- "Each request is handled in its own goroutine, so the server is concurrent by default."
