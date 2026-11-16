# Observability (CloudWatch)

## Core ideas
- **Observability** = answering "what is my system doing, and why did it break?" from the outside.
- **Three pillars:**
  | Pillar | What | Ours |
  |---|---|---|
  | **Logs** | events with details ("GET /folders → 500, 39 ms") | JSON `slog` lines → CloudWatch Logs |
  | **Metrics** | numbers over time ("5xx per minute") | metric filter on our logs |
  | **Traces** | one request's path across services | X-Ray (stretch) |
- **Alarms** watch a metric and notify you before users complain.

## One request ID everywhere (the "order number")
- **Problem:** each request had two IDs: Lambda's (`RequestId:` on START/END/REPORT) and chi's (`169.254…/CMB5…-000004`) in our JSON line. Like the kitchen and cashier writing different order numbers on one order.
- **Fix** (`cmd/lambda/main.go`, `withLambdaRequestID`): on Lambda, set `X-Request-Id` to Lambda's `AwsRequestID` before chi runs (chi reuses an incoming `X-Request-Id`). Now every log line of a request carries the same ID.
  - It always overwrites a client-sent value (clients could spoof IDs).
  - It returns the ID as an `X-Request-Id` response header: the customer's "receipt".
- **Use it:** a user reports a failure → get the ID (response header, or show it in the UI) → CloudWatch → log group → **Search all log streams** → paste the ID → only that request's lines (START, our JSON, END, REPORT).
- Real run: `curl -si $API/folders | grep -i request-id` → `x-request-id: f9afd0ba-a675-466a-a187-1a866fe33297`.
- **Interview line:** "I propagate one correlation ID per request (Lambda's request ID) into every log line and return it to the client, so a support ticket maps straight to the logs."
- **Gotcha:** searching `f9afd0ba-a675-…` unquoted found nothing. In CloudWatch filter syntax a leading `-` means **exclude** ("`-debug`" = lines without debug), so dashes split and negate the ID. **Quote it:** `"f9afd0ba-a675-466a-a187-1a866fe33297"`. Quote any term with `-`, `:`, `/` or spaces.
- CLI equivalent: `aws logs filter-log-events --log-group-name /aws/lambda/notes-app-api --filter-pattern '"<id>"' --start-time <ms>` → returned START, our JSON line, END, REPORT (Duration 3.88 ms).

## Logs Insights (console: "View in Logs Insights", now also inside "Log Analytics")
- Plain search *finds* lines; Insights *analyzes* them (SQL-like). JSON log fields become columns automatically. That's why structured (JSON) logging matters.
- Scope: **Log group name** → `/aws/lambda/notes-app-api` (prefix / all groups = query many services at once). Language: Logs Insights QL.
```
fields @timestamp, method, route, status, latencyMs
| filter msg = "request"
| stats count() as requests, avg(latencyMs) as avgMs, max(latencyMs) as maxMs by route, status
| sort requests desc
```
### My real result (12 h, 2026-11-16)
- 414 lines scanned → 102 request lines → 22 route+status groups. The histogram shows each burst: console tests, smoke tests, browser clicking, pipeline runs.
- Warm reads `/folders/{folderId}/notes` avg **4.7 ms**; `POST /folders` avg 268 ms, max 518 ms (often the first call on a fresh instance: DNS + TLS to DynamoDB).
- 400/404 rows = smoke.sh's intentional bad-input checks (client errors, not bugs). **Zero 5xx.**
- **High-cardinality bug spotted:** CORS preflights (`OPTIONS` → 204) are answered before chi routes them, so the log fell back to the raw URL with real IDs (`/folders/3045330f-…/notes/a5b7b816-…`). Each ID became its own row. Group-by fields need a small, fixed set of values (route patterns), never unique IDs. Fix: log `route:"preflight"` / `"unmatched"` when there's no pattern.
- **Interview line:** "I log JSON with the route *pattern*, not the raw path, so Logs Insights can aggregate latency and errors per endpoint without high-cardinality noise."
