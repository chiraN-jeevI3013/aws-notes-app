# REST APIs (as built in this project)

## REST in one breath
- **Resources** are nouns in the URL (`/folders`, `/folders/{id}/notes`). **HTTP methods** are the verbs. **Status codes** say what happened. Request and response bodies are **JSON**. The server is **stateless**: each request carries everything needed.

## Our API
| Method | Path | Success | Errors |
|---|---|---|---|
| GET | `/health` | 200 | |
| POST | `/folders` | **201 Created** + body | 400 bad name |
| GET | `/folders` | 200 | |
| DELETE | `/folders/{id}` | **204 No Content** | 404 |
| POST | `/folders/{id}/notes` | 201 | 400, 404 folder |
| GET | `/folders/{id}/notes` | 200 (+ presigned photo URLs) | 404 |
| PUT | `/folders/{id}/notes/{noteId}` | 200 + updated note | 400, 404 |
| DELETE | `/folders/{id}/notes/{noteId}` | 204 | 404 |
| POST | `.../photo-url` | 200 `{uploadUrl, key}` | 400 type/size, 404 |
| POST | `.../photos` | 200 note | 400 bad/foreign/missing key |

- **Idempotent** = doing it twice has the same effect as doing it once: GET, PUT, DELETE. **POST isn't**: two POSTs create two folders.
- **PUT vs PATCH:** PUT replaces the resource (we send title + text). PATCH sends only the changed fields.
- **400 vs 404 vs 500:** 400 = the client sent bad input. 404 = the thing doesn't exist. 500 = our bug or a dependency failure (logged with requestId, never leaked to the client).

## Why look at the preflight in DevTools?
1. **One `fetch(PUT)` = two requests.** Your code calls fetch once. The browser quietly sends an `OPTIONS` request first, and only if that's approved does it send the PUT. You never wrote that OPTIONS call, which is why people get confused by it.
2. **Real-world CORS debugging.** When a team says "the API works in curl but not in the browser," 90% of the time the **preflight** is failing: the server doesn't handle OPTIONS, or it's missing an allow header. In the Network tab, the OPTIONS row is red and the PUT never happens.
3. **It costs time.** Every preflight is an extra round trip. In prod, add `Access-Control-Max-Age` so the browser caches the approval.
4. **It sets up Phase 5.** API Gateway must answer OPTIONS correctly, or the deployed page breaks.

## What my DevTools showed when I clicked the "Trips" folder (3 requests)
| # | Request | Result | Why |
|---|---|---|---|
| 1 | `GET :8080/folders/<id>/notes` | 200 | **No preflight**: a plain GET with no custom headers is a "simple request". It still carries `Origin:`, and the API answers with `Access-Control-Allow-Origin`. |
| 2 | `GET :4566/.../e1edf66f...jpg` | **404 NoSuchKey** → `[missing photo]` | A **dangling reference**: the key is in DynamoDB, but the file was wiped when the LocalStack container was recreated. |
| 3 | `GET :4566/.../756012f1...jpg` | 200 → thumbnail | A valid presigned GET (`X-Amz-Expires=3600`, `SignedHeaders=host`). |

**Why 1 API call + N image calls?** The notes `GET` returns **JSON with a URL per photo, not the image bytes**. The browser then creates `<img src=url>` for each one and fetches them **directly from S3**, in parallel. The notes call must finish first, because the URLs are inside it (DevTools → Waterfall column).
- Upside: the API sends a few KB, and the heavy bytes go S3 → browser. Lambda is billed per ms, so it shouldn't stream photos.
- Catch: each listing signs **new** URLs (new date and signature), so the browser cache can't reuse them, and photos re-download on every click. Prod fix: **CloudFront** (CDN) in front of S3, with signed cookies or stable URLs, so images are cached at the edge and in the browser.

Headers worth knowing (from DevTools → "Copy as cURL"):
- `Origin: http://localhost:5500`: sent on CORS requests. It's what the server's CORS check compares against.
- `Sec-Fetch-Mode: cors` (the API fetch) vs **`no-cors`** (the `<img>` loads). Plain image loads aren't CORS reads, so they work without bucket CORS.
- `Sec-Fetch-Site: same-site`: different **ports** = different **origins** but the same **site** (`localhost`). CORS cares about origin.
- "Copy as cURL" in DevTools turns any browser request into a curl command. It's great for reproducing bugs.

## Fixing dangling references for real
- Don't delete storage independently of the DB (all deletes go through the API, which we do).
- A periodic cleanup job: for each `photoKey`, `HeadObject`, and drop the key if it's missing.
- The reverse problem (an object in S3 with no DB key, e.g. upload never confirmed): an S3 **lifecycle rule** or a sweeper job.

## Interview one-liners
- "POST creates (201), PUT replaces (200), DELETE returns 204. GET/PUT/DELETE are idempotent, POST isn't."
- "I return 4xx for client mistakes with a clear message, and 5xx only for server faults, logged with a request ID."
- "Simple GETs skip the preflight. PUT/DELETE or JSON bodies trigger an OPTIONS preflight."
