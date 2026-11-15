# Notes App: Interview Notes

## AWS services used

### DynamoDB
- **What:** A managed NoSQL key-value and document database. Every item is found by its key: a **partition key** (PK), which picks the storage partition, plus an optional **sort key** (SK), which orders items within that partition.
- **Why here:** It's serverless and pay-per-request (on-demand), so it costs pennies when idle. It's fast for the two access patterns we have: "list my folders" and "list notes in a folder."
- **Single-table design:** Folders (`PK=USER#demo, SK=FOLDER#<id>`) and notes (`PK=FOLDER#<id>, SK=NOTE#<id>`) live in one table. Each list is a single `Query` with `begins_with(SK, ...)`, with no scans and no joins.
- **Tradeoffs:** You design the keys around your queries up front. New access patterns may need a GSI. There are no ad-hoc joins. Deleting a folder means deleting its notes one by one (no cascade).
- **Local dev:** DynamoDB Local in Docker uses the same API, so the same Go code runs locally by pointing the SDK at `http://localhost:8000`.

### S3
- **What:** Object storage: files ("objects") are stored in buckets and found by key. It has a flat namespace, so "folders" are just key prefixes.
- **Why here:** It holds the note photos at `notes/<noteId>/<uuid>.<ext>`. The bucket is private.
- **Presigned URLs:** The API signs a time-limited URL with its own credentials. The browser then PUTs or GETs the photo directly to or from S3. Content type and size are part of the signature, and only jpeg/png/webp up to 5 MB are allowed.
- **Tradeoffs:** The upload takes two steps (get URL → upload → confirm), so a client could upload and never confirm, leaving an orphan object. Fix: an S3 lifecycle rule or a cleanup job. Deleting photos is best-effort after the DB delete (it's logged if it fails).

## Design decisions and tradeoffs
- **Conditional writes:** Updating a note uses `ConditionExpression: attribute_exists(PK)`, so updating a missing note returns 404 instead of silently creating a new item (DynamoDB writes are upserts by default).
- **Structured JSON logs:** There's one log line per request with requestId, route pattern, status, and latency. CloudWatch Logs Insights can query those fields directly.
