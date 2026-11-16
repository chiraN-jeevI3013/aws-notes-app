# CI/CD (CodePipeline + CodeBuild)

## Core ideas
- **CI (Continuous Integration):** every push is automatically vetted and tested. **CD (Continuous Delivery/Deployment):** if it passes, it ships automatically.
- Goal: **`git push` is the only deploy step.** No hand-uploaded zips.
- **CodeConnections:** secure AWS ↔ GitHub link via a GitHub App installed on *only this repo*. The number shown (e.g. `182417966`) is the **installation ID**: your copy of the "AWS Connector for GitHub" app. It acts as a bot, not as you. Not a secret.
- **CodePipeline** = the conveyor belt (stages). **CodeBuild** = a temporary Linux container that runs `buildspec.yml`, then is thrown away (billed per build-minute).
- **Each service has its own role:** pipeline role (move artifacts, start builds) ≠ build role (deploy only our Lambda + `index.html`) ≠ Lambda execution role (DynamoDB, S3, logs).

## Our pipeline
```
git push (main) ──webhook──► Source (GitHub App → zip → artifact S3 bucket)
                               └──► Build (CodeBuild notes-app-build runs buildspec.yml)
                                      install:    Go 1.27.4
                                      pre_build:  go vet + go test        ← fail = nothing deploys
                                      build:      build-lambda.sh → zip
                                      post_build: update Lambda → wait → stamp SHA in index.html → S3 → smoke.sh on live API
```
- Proof it works (2026-11-16): trigger `WebhookV2` on commit `0a6eacb`; live page `<meta name="version" content="0a6eacb">`; smoke 13/13 against production.

## Console choices and why
| Setting | Choice | Why |
|---|---|---|
| Pipeline type | V2 | triggers with branch/file filters; pay per action-minute |
| Execution mode | **Superseded** | a newer push replaces an older waiting run. Queued = every commit in order; Parallel = independent runs (risky for deploys) |
| Service role checkbox | ticked | lets CodePipeline create its own role |
| Artifact store | default S3 bucket `codepipeline-us-east-2-…` | passes the source zip between stages (delete at teardown) |
| Output artifact format | CodePipeline default (zip) | Full clone only if the build needs git history |
| Trigger | **Push** on `main`; exclude `Study-Notes-Interview/**,**/*.md` | without a filter, **pull requests would deploy unreviewed code**. Notes-only pushes skip the build. Exclude only applies when *all* changed files match |
| Auto-retry | off for Build | retries help flaky failures; failing tests don't fix themselves |
| Build provider | CodeBuild (not "Commands") | Commands = inline shell in the pipeline. CodeBuild project + `buildspec.yml` in the repo = **pipeline-as-code**, own role, env vars, logs |
| Test / Deploy stages | skipped | our buildspec tests and deploys (see tradeoff below) |
| `DetectChanges: false` | fine | old V1 polling flag; V2 uses the trigger |
| Compute | 4 GB / 2 vCPU (smallest) | Go builds are light |

## "Why are Build/Test/Deploy optional?"
- CodePipeline is generic; only Source is required. A static site can be Source → Deploy (S3) with no build.
- **Deploy inside the build (ours)** vs **separate Deploy stage:**
  | | Ours | Separate stage |
  |---|---|---|
  | Simplicity | one place, one log | needs artifacts + one deploy action per target |
  | Permissions | build role holds deploy rights | build role only builds |
  | Approvals | none | manual approval before prod |
  | Rollback | broken code stays live if smoke fails | "automatic rollback on stage failure" |
- Interview line: "For simplicity I deploy from the build and smoke-test after. In production I'd split Build and Deploy, add a manual approval, and use Lambda versions + aliases with CodeDeploy canary (10% → 100%) so a failed health check rolls back automatically."

## Project vs buildspec (why a push didn't fix the first failure)
| Thing | What | Lives in |
|---|---|---|
| `buildspec.yml` | the **recipe** (commands) | Git |
| CodeBuild **project** `notes-app-build` | the **kitchen** (machine, image, role, env vars, logs) | AWS, created in the console |
- The pipeline only stores a *reference* to the project name. Typing the name in the wizard ≠ creating it: the popup must finish with **Continue to CodePipeline**.

## The 3 failures I hit, and what each taught
1. `Project cannot be found: …project/notes-app-build`: the project was never saved. Also, my commit wasn't pushed (`git log origin/main` showed the old commit), so the pipeline built a commit without `buildspec.yml`. Lesson: check `aws codebuild list-projects` and `git log origin/main -1`.
2. `env var API` instead of `API_URL` (caught in review): `$API_URL` would be empty and smoke.sh would fail after deploying.
3. `AccessDeniedException … lambda:UpdateFunctionCode`: expected. See below.

## The AccessDenied, in plain English
**The build robot tried to put the new code onto our Lambda, and AWS said "you're not allowed."**
1. CodeBuild runs as the role `codebuild-notes-app-build-service-role`: its **badge**.
2. Downloading Go, testing and zipping happen *inside* its own machine, so they need no permission.
3. Then it tried to **change something outside**: "replace `notes-app-api`'s code with this zip".
4. AWS checked the badge: no such permission. The console-created role only had basics (logs, source artifact, connection).
5. AWS **denies anything not explicitly allowed** → the build stopped. Nothing was broken; the robot just didn't have the key.
- **Fix:** attach inline policy `notes-app-deploy` (`infra/iam/codebuild-deploy.json`): UpdateFunctionCode + GetFunction/GetFunctionConfiguration (for `wait`) on `notes-app-api`, PutObject on `notes-app-web-cj/index.html`. Nothing else.
- **A policy file in Git does nothing** until it's attached to a role in IAM.
- **Why AWS didn't add it automatically:** a buildspec is arbitrary shell; AWS can't know what it deploys. Auto-granting broad rights would make every build robot an admin (a compromised dependency could wreck the account). Default to nothing; the owner grants exactly what's needed.
- Retry without a new push: pipeline → Build stage → **Retry** (same commit).

## Useful commands
```bash
aws codebuild list-projects --output text                       # does the project exist?
aws codepipeline list-pipeline-executions --pipeline-name notes-app-pipeline --max-items 3 \
  --query 'pipelineExecutionSummaries[].[status,trigger.triggerType,sourceRevisions[0].revisionId]' --output text
infra/create-resources.sh pipeline                               # connection, stage states, live version
git log origin/main --oneline -1                                 # what GitHub actually has
```

## Ideas for later
- **Cache Go modules** (`~/go/pkg/mod`): `go vet` spent ~46 s downloading modules in a fresh container every build.
- Separate Deploy stage + approvals + Lambda aliases/canary (above).

## Interview one-liners
- "Push to main triggers CodePipeline via a GitHub App connection; CodeBuild vets, tests, builds an arm64 zip, deploys, and smoke-tests the live API. Failing tests stop the deploy."
- "Every actor has its own least-privilege role: the build can update one function and one file, nothing else."
- "I filter triggers to pushes on main so pull requests never deploy unreviewed code."
- "I stamp the commit SHA into the frontend so I can always tell which version is live."
