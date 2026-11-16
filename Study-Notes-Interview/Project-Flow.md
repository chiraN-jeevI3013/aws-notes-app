# Project Flow (living doc, updated each phase)

> Tip: the flowcharts use Mermaid. GitHub renders them directly. In VS Code, install the extension **"Markdown Preview Mermaid Support"**, then press `Cmd+Shift+V`.

## 1. The final target (what we're building toward)
```mermaid
flowchart LR
    U[You in a browser] -->|loads page| FE[S3 static website<br/>index.html]
    U -->|fetch JSON| APIGW[API Gateway<br/>HTTP API]
    APIGW --> L[Lambda<br/>Go code]
    L -->|read/write| DDB[(DynamoDB<br/>table notes-app)]
    L -->|signs a URL| U
    U -->|PUT/GET photo directly<br/>with presigned URL| S3P[(S3 photo bucket<br/>private)]
    L -->|logs| CW[CloudWatch]
    GH[GitHub push] --> CP[CodePipeline] --> CB[CodeBuild<br/>go test + build zip] -->|update code| L
```

## 2. Where we are now: Phase 1 (local only, nothing on AWS yet)
```mermaid
flowchart LR
    S[scripts/smoke.sh<br/>curl requests] -->|HTTP :8080| G
    subgraph Mac[Your Mac]
      G[Go server<br/>cmd/server/main.go<br/>runs directly with go]
      subgraph Docker[Docker Desktop]
        D[(Container: dynamodb-local<br/>image amazon/dynamodb-local<br/>listens on 8000)]
      end
    end
    G -->|AWS SDK calls<br/>DYNAMODB_ENDPOINT=http://localhost:8000| D
```

### How a single request flows through the code
```mermaid
flowchart TD
    R[HTTP request<br/>POST /folders] --> MW[middleware.go<br/>RequestID -> log -> recover -> CORS]
    MW --> RT[router.go<br/>chi picks the handler by method+path]
    RT --> H[handler createFolder<br/>decode JSON, validate]
    H --> ST[store.go CreateFolder<br/>PutItem PK=USER#demo SK=FOLDER#id]
    ST --> DB[(DynamoDB / DynamoDB Local)]
    H --> RESP[201 + JSON response]
    MW --> LOG[one JSON log line:<br/>requestId, route, status, latencyMs]
```

## 3. Phase 2: photo upload (presigned URLs, LocalStack locally)
```mermaid
sequenceDiagram
    participant B as Browser / curl
    participant API as Go API :8080
    participant DB as DynamoDB Local :8000
    participant S3 as LocalStack S3 :4566
    B->>API: POST .../photo-url {contentType, size}
    API->>API: sign URL locally (no network call!)
    API-->>B: {uploadUrl, key}
    B->>S3: PUT uploadUrl (photo bytes)
    S3-->>B: 200
    B->>API: POST .../photos {key}
    API->>S3: HeadObject key (did it really upload?)
    API->>DB: UpdateItem: append key to photoKeys
    API-->>B: note with photoKeys
    B->>API: GET .../notes
    API-->>B: notes + presigned GET URL per photo
    B->>S3: GET photo URL (show thumbnail)
```

## 4. Phase 3: everything in Docker
```mermaid
flowchart LR
    C[curl / browser<br/>on your Mac] -->|localhost:8080| API
    C -->|localhost:4566<br/>presigned URLs| LS
    subgraph Net[Docker network: cj-fun-aws-notepad-app_default]
      API[api container<br/>Go binary, distroless, non-root]
      DDB[(dynamodb container)]
      LS[(localstack container<br/>S3)]
      API -->|http://dynamodb:8000| DDB
      API -->|http://localstack:4566| LS
    end
```

## 5. Phase 5: live on AWS
```mermaid
flowchart LR
    B[Browser] -->|1 HTTP: index.html| WEB[(S3 website bucket<br/>notes-app-web-cj, public)]
    B -->|2 HTTPS fetch| APIGW[API Gateway HTTP API<br/>$default route, throttling]
    APIGW -->|3 event JSON| L[Lambda notes-app-api<br/>cmd/lambda → adapter → chi]
    L -->|4 role creds| DDB[(DynamoDB notes-app)]
    L -->|presign| S3[(S3 notes-app-photos-cj<br/>private)]
    B -->|5 PUT/GET presigned| S3
    L -.->|logs| CW[CloudWatch Logs]
```
| Local (Phase 3–4) | AWS (Phase 5) |
|---|---|
| `python -m http.server :5500` | S3 static website (HTTP) |
| browser → Go on `:8080` | browser → API Gateway (HTTPS :443) → Lambda |
| `cmd/server` (ListenAndServe) | `cmd/lambda` (lambda.Start + httpadapter) |
| DynamoDB Local / LocalStack | real DynamoDB table / S3 bucket |
| fake `test/test` creds | execution role `notes-app-lambda-role` |
| `docker compose logs` | CloudWatch Logs `/aws/lambda/notes-app-api` |
- **Who checks permission at each hop:** API Gateway → Lambda = resource-based policy (only our API). Lambda → DynamoDB/S3 = execution role. Browser → API = CORS (Go middleware). Browser → photo bucket = bucket CORS + presigned signature. Browser → website = bucket policy (public GetObject).

## Phase log
| Phase | What changed in the flow |
|-------|--------------------------|
| 1 Local API | Go server on the Mac talks to DynamoDB Local in Docker on port 8000 |
| 2 Photos | + LocalStack (S3) on 4566. The API only *signs* URLs; photo bytes go browser ↔ S3 directly |
| 3 Docker | The API runs as a 3rd container. Containers talk by service name; the Mac uses published ports |
| 4 Frontend | `web/index.html` served on `:5500` calls the API `:8080` (CORS: Go middleware) and S3 `:4566` (CORS: bucket rule) |
| 5 Deploy | Frontend on an S3 website, API Gateway → Lambda (same chi router via adapter) → real DynamoDB + S3; least-privilege role; smoke 13/13 + photo 9/9 on AWS; browser end-to-end verified |
