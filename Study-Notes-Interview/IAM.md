# IAM (Identity and Access Management)

## How it works

**An IAM user is an identity: a name plus permissions.** It's like a badge for someone who works in your AWS account.
- Every account has one **root user** (the email you signed up with). It can do anything, including closing the account and changing billing.
- Best practice: lock root away (MFA, no access keys) and use it only for billing and emergencies. Do daily work as an IAM user, or better, an IAM Identity Center (SSO) user.

**Who is acting when a tool or an AI runs `aws` commands?**
- The AWS CLI uses whatever credentials are cached on the machine (for example after `aws login`).
- So any tool running commands on my laptop acts as *me*. AWS can't tell the difference.
- Every API call is recorded in **CloudTrail** under the identity that made it.

**Humans vs machines:**
- **Humans** get users or SSO identities. They sign in and get short-lived credentials.
- **Machines and services** (Lambda, CI/CD, automation tools) get **IAM roles**. A role has no password. It's *assumed* to get temporary credentials, with only the permissions the job needs (**least privilege**).
- In a real company, an automated tool gets its own role. Its actions then show up separately in CloudTrail, and the damage it can do is limited (the "blast radius").

## Scope: IAM is global and per account
- IAM belongs to the **whole AWS account** (the 12-digit account ID). It's **global**, not tied to a region, so a user created while you're in `us-east-1` exists everywhere.
- There are no "project-level" IAM users. Console features that group things (myApplications, resource groups, tags) are just *views* over resources. The real hard boundary is the **account**.
- That's why companies use **many accounts** under AWS Organizations: the account is the unit of isolation.

## Logging in: root vs IAM user

| | Root user | IAM user |
|---|---|---|
| Sign in with | Email + password | Account ID (or alias) + username + password |
| Has an email? | Yes, it's the login | No. IAM users don't have emails |
| MFA | Authenticator app (or a passkey or security key) | Its own MFA entry, which can be in the same authenticator app |

- MFA is tied to a **device or app**, not an email. One authenticator app can hold two entries, one for root and one for the IAM user.
- The IAM user sign-in URL looks like `https://<account-id>.signin.aws.amazon.com/console`.

## Ways to give a user permissions
- **Policy:** a JSON document listing allowed or denied actions on resources. *Managed* policies (like `AdministratorAccess`) are reusable. *Inline* policies are attached to one identity only.
- **Attach directly:** the policy goes on the user. Quick, but it doesn't scale.
- **Groups (best practice):** put the policy on a group (e.g. `Admins`, `Developers`) and add users to it. To change permissions for a whole job function, you change one place.
- **Permissions boundary:** a ceiling on the *maximum* permissions a user or role can ever have, even if a policy grants more. It's used when you let someone else create IAM users or roles without letting them escalate privileges. (Similar idea to SCPs, but for a single identity rather than a whole account.)

## Real example I hit: an SCP blocking IAM user passwords
- My account sits inside an AWS Organization (the newer AWS account setup). Its **SCP (Service Control Policy)** explicitly **denies** creating console passwords (login profiles) for IAM users.
- The result: the "Console sign-in" section was hidden, and even `AdministratorAccess` couldn't change it. **An SCP sets the maximum permissions for an account. An explicit Deny in an SCP beats any Allow in IAM.**
- Why an org would do this: it pushes humans to **IAM Identity Center (SSO)** for console access, and keeps IAM users for programmatic (CLI/SDK) access only.
- How AWS evaluates a request: **an explicit Deny anywhere wins → otherwise it needs an Allow from both the SCP *and* the IAM policy (and the boundary, if one is set) → otherwise it's an implicit deny.**
- Options I had: (1) IAM Identity Center for console and CLI with short-lived credentials (best practice); (2) access keys on the IAM user for the CLI only (quick, but long-lived, so rotate or delete them).

## My account type: "AWS (new)", which is a managed Organization
- Signing up through the new experience gave me a **managed AWS Organization**: a management account that AWS controls, plus **project accounts** (like "Building things"). These are owned by my **AWS Builder ID**, not by a root email.
- AWS applies **preconfigured SCPs and RCPs** (for example, no IAM user passwords and a `RegionFloor` that limits regions) and a **spend limit**. These are guardrails for beginners.
- Humans sign in with Builder ID and the CLI uses **`aws login`**, which gets **short-lived role credentials** from the browser. There's no need for IAM users or access keys.
- "Activate advanced features" (this **can't be undone**) hands me the full Organization: I manage SCPs and RCPs, IAM Identity Center, the management account, all regions, and Budgets. The spend limit goes away.
- It's a real-world example of org-level guardrails: the **management account owns policy**, **member accounts run workloads**, and SCPs limit what member accounts can do.

## Debugging an AccessDenied (real example)
- `aws dynamodb list-tables` in us-east-1 failed with: *"...with an explicit deny in a **service control policy**"*. The error message tells you **which layer** denied the call (identity policy, resource policy, SCP, or permissions boundary). Always read it.
- The cause was a region-restriction SCP: my project only allows **us-east-2**. I tested DynamoDB region by region to find the allowed one.
- S3 `list-buckets` and IAM still worked from us-east-1 because they're **global** services. A region SCP usually exempts global services.
- My CLI identity is an **assumed role** (`arn:aws:sts::<acct>:assumed-role/AccountFullAccessRole/...`). `aws login` gave me temporary STS credentials for a role, not a user. You can check this with `aws sts get-caller-identity`.

## Interview one-liners
- "Root is for break-glass only: MFA on, no access keys."
- "Humans get SSO or users. Workloads get roles with temporary credentials."
- "Least privilege: grant only the actions and resources a job needs."
- "CloudTrail answers the question 'who did what, and when.'"

## Credential provider chain (bug I hit in Phase 5)
- AWS CLIs and SDKs look for credentials **in order**, and the **first match wins**:
  1. Environment variables: `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`
  2. Shared config/credentials files (`~/.aws/`), including the `aws login` / SSO cache
  3. Container or instance role (ECS task role, EC2 instance profile), or Lambda's execution role (injected as env vars)
- **What happened:** I had `export AWS_ACCESS_KEY_ID=test ...` left over from LocalStack testing. After `aws login`, the CLI still used the env vars → **`InvalidClientTokenId`** from real AWS.
- **Fix:** `env | grep AWS_` → `unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN` (or open a new terminal tab). `infra/config.sh` now refuses to run with fake creds.
- **Debug first move:** `aws sts get-caller-identity` = "who am I right now?"
- **Interview line:** "Env vars override profiles. In code, never hard-code keys; let the SDK's default chain find the role."

## Phase 5.2: the Lambda's execution role (built in the console)
- **Role** = an identity with no password/keys. Whoever *assumes* it gets temporary creds. Our Lambda runs as `notes-app-lambda-role`.
- **Trust policy** = WHO may assume it → `"Principal": {"Service": "lambda.amazonaws.com"}` (`infra/iam/lambda-trust.json`).
- **Permissions policy** = WHAT it can do → inline policy `notes-app-access` (`infra/iam/lambda-permissions.json`):
  | Sid | Actions | Resource |
  |---|---|---|
  | NotesTable | Get/Put/Update/DeleteItem, Query (no Scan!) | `table/notes-app` only |
  | PhotoObjects | Put/Get/DeleteObject | `notes-app-photos-cj/notes/*` only |
  | PhotoBucketList | ListBucket | the bucket (so missing keys → 404, not 403) |
  | OwnLogs | CreateLogGroup/Stream, PutLogEvents | `/aws/lambda/notes-app-api` only |
- **Why no managed policy?** `AmazonDynamoDBFullAccess` = every table, every action (incl. DeleteTable). Inline + exact ARNs = blast radius of one table.
- **Inline vs managed:** inline lives inside one role (deleted with it); managed is reusable across roles (AWS-managed or customer-managed).
- **Console "Access level: Limited"** = only some actions of that service → good sign. "Full access" in a review = red flag.
- **Default deny:** anything not allowed (474 other services) is denied. An explicit `Deny` beats any `Allow` (that's how SCPs like `RegionFloor` work).
- **Public repo trick:** policy JSON has `ACCOUNT_ID`/`PHOTO_BUCKET` placeholders; `infra/create-resources.sh print-policy` fills them via `sts get-caller-identity`.
- **Interview line:** "Each function gets its own execution role, trusted only by lambda.amazonaws.com, with an inline policy scoped to exact table and prefix ARNs. No long-lived keys anywhere: the SDK picks up the role's temporary creds automatically."

## Identity-based vs resource-based policies (Phase 5.4)
| | Identity-based | Resource-based |
|---|---|---|
| Attached to | a role/user | the resource (Lambda, S3 bucket, SQS queue…) |
| Answers | what can **this identity** do? | **who** can access **this resource**? |
| Ours | `notes-app-access` on `notes-app-lambda-role` (logs, DynamoDB, S3) | on `notes-app-api`: API Gateway may `lambda:InvokeFunction` |
| Where (console) | Lambda → Configuration → Permissions → top ("Execution role") | same page, bottom ("Resource-based policy statements") |
```
Browser → API Gateway ─[resource policy: may APIGW invoke me?]→ Lambda ─[execution role: may I query?]→ DynamoDB
```

## Confused deputy
- **Deputy** = a trusted service acting on someone else's behalf (API Gateway, S3, SNS…).
- **Attack:** policy trusts the whole service (`Principal: apigateway.amazonaws.com`) → attacker's API Gateway in *their* account points at *your* Lambda ARN → allowed. The deputy uses your trust to serve the attacker.
- **Fix:** add `Condition` `AWS:SourceArn` (and/or `AWS:SourceAccount`) → "only when acting for MY API `y17g32ddfb`".
- The name describes the attack; the condition is the protection. Analogy: "let in anyone in a courier uniform" vs "only the courier for order #123 to this office".
- **Interview line:** "When I grant a service principal access, I scope it with `aws:SourceArn`/`aws:SourceAccount` to prevent the confused deputy problem."

## How to read an IAM AccessDenied error (Phase 6, real example)
```
User:     arn:aws:sts::<acct>:assumed-role/codebuild-notes-app-build-service-role/AWSCodeBuild-…   ← WHO
is not authorized to perform: lambda:UpdateFunctionCode                                         ← WHAT action
on resource: arn:aws:lambda:us-east-2:<acct>:function:notes-app-api                             ← ON WHAT
because no identity-based policy allows the lambda:UpdateFunctionCode action                   ← WHY
```
- **WHO:** `assumed-role/<role-name>/<session>` = which role's badge was used. The session name hints at the caller (here a CodeBuild build).
- **WHAT + ON WHAT:** exactly the Action and Resource ARN to put in the fix (least privilege writes itself).
- **WHY** tells you *where* to fix it:
  | Reason text | Meaning | Fix |
  |---|---|---|
  | "no identity-based policy allows" | missing Allow on the role (default deny) | add an Allow to that role |
  | "explicit deny in an identity-based policy" | a Deny on the role | remove/narrow that Deny |
  | "explicit deny in a service control policy" | org SCP (e.g. our `RegionFloor`) | can't fix in-account; use an allowed region/service |
  | "no resource-based policy allows" | the resource's own policy (bucket/Lambda/KMS) doesn't allow you | update the resource policy |
- Evaluation order to remember: **explicit Deny anywhere wins → otherwise need an Allow → otherwise default deny.**
- A policy JSON file in the repo does nothing until it's **attached** to a role.
