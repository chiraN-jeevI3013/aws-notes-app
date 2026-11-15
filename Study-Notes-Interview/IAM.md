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
