# Git and GitHub: what goes in a repo

## What to commit vs keep out
| Commit ✅ | Never commit ❌ |
|---|---|
| Source code (`cmd/`, `internal/`, `web/`) | **Secrets**: `.env`, tokens, AWS access keys, `*.pem` |
| Build recipes (`Dockerfile`, `docker-compose.yml`, `buildspec.yml`) | **Build output**: binaries, `bootstrap`, `*.zip` (CI rebuilds them) |
| Dependency lockfiles (`go.mod`, `go.sum`) | Dependency folders (`vendor/`, `node_modules/`) unless required |
| Infra scripts and IAM policy JSON (`infra/`) | OS and editor junk (`.DS_Store`, `.vscode/`) |
| Docs and notes | Personal or account-specific files in a **public** repo |
| `.gitignore` itself | Large binary files (use releases or S3) |

- **`.gitignore`** lists patterns git won't track. Check with `git status --ignored`.
- **Secrets in compose are referenced, not stored:** `docker-compose.yml` has `${LOCALSTACK_AUTH_TOKEN:-}`, and the value lives only in `.env`. This is the pattern: the *config shape* in git, the *values* elsewhere (`.env` locally, Secrets Manager/SSM in AWS, CI secret variables).
- **If a secret ever gets pushed:** **rotate it immediately** (revoke and create a new one). Deleting the commit isn't enough: bots scrape GitHub for keys within minutes, and history and forks keep it. GitHub "push protection" blocks many known key formats.
- **AWS account IDs** aren't secrets (AWS says so), but there's no reason to publish them. That's why `CLAUDE.md` stays local in this public repo.

## The everyday loop
```bash
git status                  # what changed?
git diff                    # see the exact changes
git add <files>             # stage (choose what goes in the next commit)
git commit -m "message"     # snapshot, local only
git push                    # upload to GitHub
git log --oneline           # history
```
- `git add -A --dry-run` previews what *would* be staged. It's a good habit before the first commit of a project.
- Commit messages: imperative and specific, e.g. "Add presigned photo upload flow".

## This project's setup
- `git init -b main` (branch name `main`). The repo is **public**.
- Ignored: `.env`, `CLAUDE.md`, `Temp/`, `AWS-Docs/`, build output.
- Remote: GitHub over HTTPS. macOS keychain (`git-credential-osxkeychain`) supplies the GitHub login.
- Phase 6 (CI/CD) connects AWS CodePipeline to this GitHub repo: **push → build → deploy**.
