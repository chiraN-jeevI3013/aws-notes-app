# CORS and how the frontend talks to the API

## Origin
- **Origin = scheme + host + port.** `http://localhost:5500` and `http://localhost:8080` are **different origins**: same host, different port.
- The **Same-Origin Policy** (built into browsers): JS on one origin can *send* requests to another origin, but the browser **won't let the JS read the response** unless the other server allows it.

## CORS (Cross-Origin Resource Sharing)
- The server opts in with response headers:
  - `Access-Control-Allow-Origin: http://localhost:5500` (or `*` = anyone)
  - `Access-Control-Allow-Methods: GET, POST, PUT, DELETE, OPTIONS`
  - `Access-Control-Allow-Headers: Content-Type`
- **Enforced by the browser, not the server.** curl and Postman ignore CORS completely. That's why our curl scripts never cared.
- **CORS is not security for your API.** It protects *users*: it stops a random website from reading your API's responses with the user's cookies. To protect the API itself, use auth.

## Preflight
- For "non-simple" requests (PUT/DELETE, or a `Content-Type: application/json` body), the browser first sends an automatic **`OPTIONS`** request: "May origin X send a POST with header content-type?"
- Our middleware (`internal/api/middleware.go`, `cors()`) answers `204` with the allow headers. Then the browser sends the real request.
- Verified with curl, pretending to be the browser:
```bash
curl -i -X OPTIONS localhost:8080/folders -H 'Origin: http://localhost:5500' \
  -H 'Access-Control-Request-Method: POST' -H 'Access-Control-Request-Headers: content-type'
# HTTP/1.1 204 No Content
# Access-Control-Allow-Headers: Content-Type
# Access-Control-Allow-Methods: GET, POST, PUT, DELETE, OPTIONS
# Access-Control-Allow-Origin: *
```

## In this project
| Who calls | Whom | CORS handled by |
|---|---|---|
| Page on `:5500` | Go API `:8080` | our Go middleware (`ALLOWED_ORIGIN`, default `*`) |
| Page on `:5500` | S3 `:4566` (presigned PUT/GET) | the **bucket's CORS config** (S3 setting, piece 3 / Phase 5) |
| Phase 5: page on S3 website | API Gateway | API Gateway CORS config + our middleware |

## Why look at the preflight in DevTools?
1. **One `fetch(PUT)` = two requests.** The browser silently sends `OPTIONS` first, and the PUT only happens if it's approved.
2. **Debugging:** "works in curl, fails in the browser" is almost always a failing preflight. In DevTools → Network, the OPTIONS row is red and the real request never appears.
3. **Cost:** every preflight is an extra round trip. `Access-Control-Max-Age` lets the browser cache the approval (our bucket rule uses `MaxAgeSeconds: 3000`).
4. **Phase 5:** API Gateway must answer OPTIONS, or the deployed page breaks.

## S3 bucket CORS (piece 3)
- The page PUTs photos straight to S3 (`:4566`), which is a third origin, so **the bucket** needs its own CORS rule. Our Go middleware doesn't help here, because S3 answers those requests, not our API.
- `infra/localstack-init.sh` sets it on startup:
```json
{"CORSRules":[{"AllowedOrigins":["http://localhost:5500"],"AllowedMethods":["PUT","GET"],"AllowedHeaders":["*"],"MaxAgeSeconds":3000}]}
```
- Verified: a preflight from `Origin: http://localhost:5500` → `200` + `Access-Control-Allow-Origin: http://localhost:5500`. From `Origin: http://evil.com` → `403`.
- `<img src=presignedUrl>` doesn't need CORS. Plain image loads aren't CORS requests; only JS `fetch` or XHR reads are.

## Experiment: breaking CORS on purpose (step 4.5)
In `docker-compose.yml` (api service), the allowed origin is now explicit instead of `*`:
```yaml
      - ALLOWED_ORIGIN=http://localhost:5500   # works
      - ALLOWED_ORIGIN=http://example.com      # broken on purpose
```
After `docker compose up -d` (recreates only the api container) with `example.com`:
| Where | What I saw |
|---|---|
| Page | the folder list is empty: "Can't reach the API: Failed to fetch" |
| Console | "blocked by CORS policy: The 'Access-Control-Allow-Origin' header has a value 'http://example.com' that is not equal to the supplied origin" |
| Network | `GET /folders`: status **200** (Chrome labels it "CORS error") |
| API logs | `"route":"/folders","status":200,"latencyMs":3`: **the server thinks all is fine** |
| curl | `curl localhost:8080/folders` returns the data. curl ignores CORS |

**Lesson:** the request **reached** the server, and the server **answered 200**. The **browser** refused to hand the response to the page's JS, because the `Access-Control-Allow-Origin` header didn't match the page's origin (`http://localhost:5500`).
- So CORS protects **users** (a malicious site can't read your API's responses in their browser). It does **not** protect the API: curl, scripts and attackers ignore it. Protecting the API = **authentication and authorization**.
- Debug tip: if the server logs 200 but the page fails, check the **Console** for CORS. The problem is the response headers, not the server logic.
- Prod practice: pin the exact frontend origin (we will set the S3 website URL in Phase 5), not `*`.

## Client vs server validation
- `required` on the title input = the **browser** blocks empty titles (UX). A title of only spaces gets past the browser, and the **API** rejects it with 400 (safety). Never trust the client alone: anyone can call the API with curl.

## Frontend basics used (vanilla JS)
- `fetch(url, {method, headers, body})` returns a Promise. `await` waits for it. `res.ok` is true for 2xx.
- **XSS safety:** user text is inserted with `textContent`, never `innerHTML`, so a folder named `<script>…` shows as text and never runs.
- `ev.preventDefault()` on form submit stops the browser's old-style full-page POST.
- `API_URL` constant: the only line that changes between local and AWS.

## Interview one-liners
- "CORS is a browser mechanism. The server declares which origins may read its responses, and the browser enforces it."
- "A preflight is an automatic OPTIONS request the browser sends before non-simple cross-origin requests."
- "`*` is fine for a public, no-cookie API. In prod I'd pin the exact frontend origin."

## On AWS (Phase 5.6): what I saw in DevTools
- **Safari hides preflights**; Chrome lists them as Type **preflight**. Use Chrome for CORS debugging.
- **Which requests get a preflight?** Anything not "simple". Simple = GET/HEAD/POST with only basic headers (Content-Type limited to form/text types).
  | Request | Preflight? | Why |
  |---|---|---|
  | `GET /folders` | no | GET, no custom headers |
  | `POST /folders` | yes | `Content-Type: application/json` isn't simple |
  | `PUT .../notes/<id>` (save) | yes | PUT is never simple (+ JSON) |
  | `DELETE ...` | yes | DELETE is never simple |
- Preflights are **cached per URL**, so each note's first save gets its own preflight (named after the note ID). Without `Access-Control-Max-Age`, Chrome caches ~5 s. Prod: set e.g. `Max-Age: 600` to save a round trip (~100 ms here) per call.
- The photo PUT's preflight is answered by **S3's bucket CORS**, not our API (two separate CORS configs: API middleware + bucket).
- Clearing the Network tab only clears DevTools' log. Refresh → `GET /notes` (DynamoDB) → fresh presigned URLs → `<img>` loads from S3. The data persists on AWS, not in the browser.
- Tip: tick **Keep log / Preserve log** to keep requests across reloads.
