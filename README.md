# loop-o-matic

An AI Agent Loop orquestrator to run development loops.

---
>Made with 💙 by Ricardo Markiewicz // [@gazeria](https://twitter.com/gazeria).

The MVP does not store tokens: it uses `git`, `gh`, and `acli`, assuming you are already logged in.

## Requirements

- **Go**: `1.24+`
- **Git**: `2.51+`
- **GitHub CLI (`gh`)**: `2.87+`
- **Atlassian CLI (`acli`)**: `1.3+`

## Build

```bash
make build
```

Or manually:

```bash
go build -o ./loop ./cmd/loop
go build -o ./loopd ./cmd/loopd
```

## Config

By default, it reads `~/.loop-o-matic/config.yaml`. You can also use `LOOPOMATIC_CONFIG`.

```bash
mkdir -p ~/.loop-o-matic
cp config.example.yaml ~/.loop-o-matic/config.yaml
```

The app already knows the `acli` commands for Jira work items. The configuration only needs to specify the CLI:

```yaml
jira:
  cli: acli
```

Daemon concurrency:

```yaml
daemon:
  tick_interval: 30s
  max_running_tasks: 2
  max_auto_retries: 3
```

`max_running_tasks` limits how many tasks can execute real work at the same time. Long-running states such as implementing/verifying/creating a PR keep a slot while they run. Waiting states like CI/review do not keep a slot between ticks, but each active poll to GitHub/reviews briefly consumes a slot while querying and deciding what to do.

When CI fails, `loopd` writes `ci-failures.md` containing the failed checks reported by GitHub and their links. The next pass of `opencode` receives this file in its prompt to fix the failure with concrete context.

If GitHub marks the PR as out-of-date with the base branch, `loopd` runs `git fetch origin <base>`, merges `origin/<base>` into the loop's branch, pushes, and resumes monitoring CI. If there is a conflict or the merge fails, it pauses the loop for human intervention.

When a review requests changes or new human feedback appears in general comments/reviews/latest reviews of the PR, `loopd` reads the feedback using `gh`, filters out bots/CI, writes `pr-feedback.md`, and the next pass of `opencode` receives that feedback in its prompt to address the reviewer's comments. The same feedback is not reprocessed on every tick because a hash is stored per repo.

`max_auto_retries` limits how many times a loop can automatically return to the implementation phase after a failed CI or a review with changes. Once the limit is exceeded, the loop is set to `paused` until a human runs `loop resume ISSUE-123`; resuming resets the counter.

## Usage

In one terminal:

```bash
loopd start
```

In another:

```bash
loop doctor
loop start SDK-123
loop start SDK-123 --extra "Prefer minimal API changes and keep binary compatibility"
loop start SDK-123 --extra-file notes.md
loop extra SDK-123 "Also keep the migration path simple"
loop task --repo android-sdk "add support for X"
loop android-sdk "add support for X"
loop status SDK-123
loop logs SDK-123 --follow
loop delete TASK-20260616-005950 --force
loopd attach SDK-123
```

## Delete Tasks

```bash
loop delete TASK-20260616-005950
loop delete TASK-20260616-005950 --force
loop delete TASK-20260616-005950 --keep-workspace
```

By default, it deletes the SQLite state, events, issue log, worktrees, and run workspace. It does not delete active loops or worktrees with changes without `--force`.

## Jira Tickets With Extra Instructions

To add context that does not belong to the ticket itself but should be included in the agent's prompt:

```bash
loop start MOBILE-17686 --extra "Prefer the smallest API surface and keep existing behavior compatible"
loop start MOBILE-17686 --extra-file local-notes.md
```

You can also add instructions to an already created or running loop:

```bash
loop extra MOBILE-17686 "Also avoid changing the public API unless strictly necessary"
loop extra MOBILE-17686 --file follow-up-notes.md
```

Extra instructions accumulate in `extra-instructions.md` within the run directory and are injected into implementation, verification, and PR metadata prompts. If `opencode` is already running, the new instructions will apply in the next pass of the loop.

## Tasks Without Jira

To launch a loop without an actual ticket:

```bash
loop task "add something and validate the tests"
```

To specify the repo/application:

```bash
loop task --repo android-sdk "add something"
loop task --repos android-sdk,ios-sdk "maintain parity of this API"
loop task --all-repos "find where it applies and fix it"
```

Shortcut if the first argument matches a configured repo:

```bash
loop android-sdk "add something"
```

This creates a local fake-ticket `TASK-yyyymmdd-hhmmss` and processes it exactly like a Jira ticket.

## MVP Flow

1. `loop start ISSUE` reads Jira using `acli`, creates SQLite state, and registers the loop.
1. `loop task ...` creates a local fake-ticket without touching Jira.
2. `loopd start` prepares isolated workspaces from configured bare repositories.
3. The daemon detects impact by searching for ticket tokens within the repos.
4. It invokes `opencode run --model github-copilot/gemini-3.5-flash` with a prompt built from the ticket, plan, and workspaces.
5. It invokes another pass of `opencode` in test/verification mode with a specific prompt to decide and execute tests/build/lint/typecheck depending on the repo and the change.
6. It creates branches/commits/PRs using `git` and `gh`.
7. It monitors CI and human review until completion.

If `executor.command` is configured, it is used as an escape hatch instead of the default `opencode` command.

If a bare repo is missing, `loopd` clones it automatically using `repos.<name>.github`:

```yaml
repos:
  red-balloons:
    bare: red-balloons.git
    github: owner/red-balloons
```

`owner/repo` is converted to `git@github.com:owner/repo.git`. You can also provide a full URL.

## Branch Naming

Before creating each worktree, `loopd` asks `opencode` for a specific branch name for the task.

Rules:

- Include the type of work: `feat`, `fix`, `chore`, `docs`, `refactor`, `test`, `perf`, `ci`, `build`.
- Include the ticket/story id if it exists, in lowercase.
- For local tasks `TASK-*`, include the task id in lowercase.
- Include a short, imperative present-tense description in English.
- Do not copy raw text from the request if it is in Spanish or another language; translate and summarize the change in English.
- Use hyphens to separate words.
- Format: `{type}/{jira_ticket}-{title_description}`.

Examples:

```text
feat/mobile-1234-collect-network-api-error
fix/sup-1234-sr-masking-not-working
feat/task-20260616-005950-configure-action-keybindings
```

If `opencode` returns something invalid or copies non-English words, `loopd` makes a second attempt with a stricter repair prompt. If it still fails, it uses a deterministic English fallback based on task signals, avoiding generic names like `implement-requested-change`.

## Commit And PR Metadata

Before committing or creating a PR, `loopd` asks `opencode` for repo-specific metadata in JSON:

```json
{
  "title": "feat: configure action keybindings",
  "commit_body": "Adds configurable shortcuts for each action.",
  "pr_body": "## Summary\n- ...\n\n## Verification\n- ..."
}
```

Rules:

- The commit and PR titles must follow Conventional Commits.
- The title, commit body, and PR body must always be in English.
- They must not copy raw text from the request if it is in Spanish or another language; they must summarize the change in English.
- The PR body must be detailed and include Summary, Detailed Changes, Verification, and Risks / Notes.
- The PR body must not include local workspace paths; `loopd` reads the ticket, plan, summaries, and diff, and passes that content to `opencode` to generate human-readable text.
- The PR body must always end with `Co-authored-by: loop-o-matic` and `Generated-with: <model>`.
- Allowed types: `feat`, `fix`, `chore`, `docs`, `refactor`, `test`, `perf`, `ci`, `build`.
- If `opencode` fails or returns something invalid, `loopd` uses a deterministic English fallback.
