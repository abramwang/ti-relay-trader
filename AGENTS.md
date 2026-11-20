# Repository Workflow

## Git Completion Policy

- The user requires a local Git commit at the end of each functional iteration.
  Do not leave completed core work merely modified or untracked. A commit does
  not authorize a push, release, migration, deployment or environment switch.
- Inspect `git status --short --untracked-files=all` before editing. Preserve
  unrelated user work; never revert it or include it without authorization.
- Track the iteration's source, migrations, tests, public schemas, SDK changes,
  reproducible scripts and maintained documentation. Describe incomplete or
  disabled features accurately; committing source does not mean it is deployed.
- Before committing, review the diff and file list, run relevant tests and
  `git diff --check`, and check for secrets and private data. Stage explicit
  paths and use coherent feature-sized commits instead of blindly adding all.
- Keep credentials, private configuration, broker exports under `reference/`,
  `.runtime/`, local evidence/backups, logs and build artifacts out of Git.
  Do not force-add ignored files or remove local data to make status clean.
- After committing, inspect the commit summary and remaining status. Report
  commit IDs, verification results and any intentionally uncommitted files.
  If a commit cannot be completed, explain the concrete blocker to the user.

See README.md for operating state and docs/ROADMAP.md for development priorities.
