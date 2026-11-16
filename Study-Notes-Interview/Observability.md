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

## Metric filter: logs → number
- `/aws/lambda/notes-app-api` → Metric filters → pattern **`{ $.status >= 500 }`** (`{}` = parse as JSON; START/END/REPORT lines aren't JSON → ignored).
- Namespace `NotesApp`, metric `Api5xx`, value `1` per match, **default value `0`** (minutes with logs but no errors report 0).
- **No dimensions:** each unique dimension value is a separately billed metric (~$0.30/month each). Never use IDs as dimensions (high cardinality). Default value and dimensions are mutually exclusive anyway.
- A custom metric **doesn't exist until its first data point**: create the filter, then send traffic, then it appears (~45 s for me) under *Custom namespaces → NotesApp*.

## Alarm settings explained (smoke-detector analogy: what it senses, how often, how much, what if silent)
| Setting | Ours | Meaning |
|---|---|---|
| Data source | Metrics | watch a number (Logs = scheduled log query; Alarms = composite "A and B") |
| Statistic | **Sum** | count errors per period. **Average is a trap:** values are 1 (error) and 0 (default per log line), so 1 error in 100 requests averages 0.01 and never reaches 1 |
| Period | 1 minute | size of each time slice checked |
| Threshold | Static, **≥ 1** | 1+ errors in a minute → ALARM. (Anomaly detection = AWS learns "normal", needs history) |
| Datapoints to alarm | **1 of 1** | one bad minute triggers. "3 of 5" ignores one-off blips (less flapping) |
| Missing data | **treat as good** | no traffic = no errors. As missing → grey "Insufficient data"; as bad → for heartbeats ("alert if the job stops reporting") |
| Evaluation window | sliding (default) | last N minutes from now (vs fixed clock-aligned buckets) |
| Warm-up | off | waits for first data before evaluating new metrics |
- **States:** OK (green) / ALARM (red → notify) / Insufficient data (grey).
- Preview graph: blue line = metric, red dashed line = threshold.
- **Interview line:** "I alarm on the Sum of a 5xx count metric derived from structured logs, 1-minute periods, missing data treated as not breaching, and notify via SNS. For noisy systems I'd use M-of-N datapoints or alarm on error rate instead of count."
- **Alarm description = runbook** sent in the notification email: what it means + first 3 steps (Logs Insights filter `level = "ERROR"`, check recent deploys/env vars, run smoke.sh). Markdown renders only in the console.

## Game day (2026-11-16): break → alert → investigate → fix → recover
1. **Break:** Lambda env `TABLE_NAME=notes-app-broken` (simulated bad config change). `/health` stayed 200 (it doesn't touch DynamoDB): a health check alone can lie.
2. **Errors:** 5 × `GET /folders` → `HTTP/2 500`, each with an `x-request-id`.
3. **Alert:** ~1 min later the alarm went OK → ALARM; email: "1 out of the last 1 datapoints [5.0 (06:39:00)] ≥ 1.0". `5.0` = Sum of 5 errors (Average would never have fired). The runbook description arrived in the email.
4. **Investigate:** Logs Insights
   ```
   fields @timestamp, route, status, requestId, err
   | filter level = "ERROR"
   | sort @timestamp desc
   ```
   → 2 ERROR lines per request, same requestId: `internal error` + `err` (the **why**) and `request /folders 500` (the **what**).
   One request's full story:
   ```
   fields @timestamp, @message
   | filter @message like "43624e2e-d7f0-4edc-8a8b-5831be531843"
   | sort @timestamp asc
   ```
   → START → internal error → request 500 → REPORT (6.38 ms) → END.
5. **Surprise:** the error was **`AccessDeniedException`** (not `ResourceNotFoundException`): `notes-app-lambda-role … not authorized to perform dynamodb:Query on table/notes-app-broken`. **IAM is evaluated before the resource lookup**; our role only allows `table/notes-app`. Least privilege held: a misconfigured or hijacked env var still can't reach other tables. Lesson: read the actual error; AccessDenied can be a symptom of a *config* change, not an IAM change (runbook step 2: check deploys **and env vars**).
6. **Fix + recover:** `TABLE_NAME=notes-app` → 200s → alarm back to OK.
- **Interview story (STAR):** "I ran a game day: broke config on purpose, got paged by a CloudWatch alarm on a log-derived 5xx metric within a minute, used the request ID from the response header to pull that request's full log timeline in Logs Insights, found the root cause, fixed it, and watched the alarm clear. It also proved least privilege: the misconfigured function was denied access to any other table."
