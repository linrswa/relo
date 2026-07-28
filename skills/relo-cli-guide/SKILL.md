---
name: relo-cli-guide
description: Operate, explain, script, integrate, and troubleshoot the relo CLI, including executable version and upgrade behavior, project metadata, task and dependency commands, legal runtime transitions, milestones, graph/status/validation views, and relo.output/v1 JSON. Use whenever a request concerns relo command syntax, supported flags, state or output interpretation, shell automation, upgrades, recovery from a CLI error, or executing an already-specified relo mutation. This is a CLI contract guide only; for PRD decomposition, delivery planning, task selection, milestone strategy, implementation verification, worker coordination, or execution policy, use relo-delivery-workflow instead.
compatibility: Requires access to a relo executable and, except for version, upgrade, and initialization, a repository initialized with .relo metadata.
---

# Relo CLI guide

Apply relo's public command contract accurately. Keep CLI mechanics separate from software-delivery decisions.

## Keep the responsibility boundary clear

Use this skill to:

- discover supported commands and flags;
- execute an operation the user has already specified;
- inspect and explain project, task, dependency, attempt, or milestone state;
- write scripts against supported JSON output;
- diagnose rejected commands and choose a legal CLI recovery path;
- inspect or upgrade the executable.

Do not use this skill to decide how to decompose a PRD, which tasks or priorities should exist, whether a milestone is strategically useful, what ready work to select, how implementation should be verified, or how workers should be coordinated. Those decisions belong to `relo-delivery-workflow` or to explicit user instructions. When both skills apply, the delivery workflow decides **what** should happen and this guide supplies **how** to express it through the CLI.

Activation of this guide does not by itself authorize subagents, task execution, upgrades, project removal, or any other mutation.

## Follow this operating procedure

1. Determine whether the user wants an explanation, a script, or actual execution. Do not mutate state for an explanation-only request.
2. Bind to one executable. Use the path supplied by the user; otherwise use `command -v relo`. Run `relo --help` and the relevant command's `--help` before relying on remembered syntax.
3. Read the relevant section of [references/command-reference.md](references/command-reference.md). If an installed executable differs from the reference, its current `--help` and observed supported behavior win; report the version mismatch rather than guessing.
4. Establish project context. Run `init` from the intended project root. Other project commands discover the nearest initialized ancestor containing `.relo/relo.db`; an empty `.relo/` directory is not initialized. `project remove` is root-only.
5. Inspect the narrowest useful state before a mutation, using commands such as `project show`, `task get`, `task ready`, `milestone get`, `status`, or `graph`.
6. Execute only supported mutations that are necessary for the request. Preserve generated IDs and non-zero failures in the report.
7. Verify through public reads. Use `validate` after structural changes, then select `task get`, `task ready`, `milestone get`, `graph`, or `status` according to what changed.

Never read, edit, query, copy as an API, or otherwise depend on `.relo/relo.db`; it is private SQLite state. Never bypass a rejected mutation through persistence edits.

## Preserve identity and command intent

- Keep CLI-emitted IDs such as `TASK-001`, `AC-001`, `NOTE-001`, `MILESTONE-001`, and `REC-001` verbatim.
- Use IDs for mutations. A task title is available only for exact, read-only `task get --title` lookup, and duplicate exact titles make that lookup ambiguous.
- Do not use a mutation as a probe. Inspect state and help first. If a requested operation is illegal, explain the constraint and the supported recovery sequence without intentionally issuing a command expected to fail.
- Treat batch failures as authoritative. Multi-task start/stop and batch dependency changes are atomic; do not report partial success when the CLI rejects the batch.
- Run destructive or environment-changing commands such as `upgrade` and `project remove --force` only when explicitly requested.

## Apply the task and dependency invariants

Stored task states are `pending`, `running`, `passed`, and `failed`. `ready` and `blocked` are derived views of pending tasks; a pending task is ready only when all dependencies have passed.

```text
pending --start--> running --pass--> passed
                         \--fail--> failed --retry--> pending
                         \--stop-------------------> pending
passed  --reopen-------------------------------> pending
```

- `start` accepts only ready pending tasks.
- `pass`, `fail`, and `stop` accept only running tasks with an open attempt.
- `retry` accepts only failed tasks; `reopen` accepts only passed tasks.
- `fail`, `stop`, and `reopen` require reasons. `pass` supports an optional evidence-based summary.
- Task definitions, acceptance criteria, notes, and deletion are mutable only while that task is pending or failed. For a dependency mutation, this rule applies to the dependent target (the first ID); the prerequisite's state does not control whether the edge can change. Stop a running target before editing it.
- Reopening passed upstream work is rejected while downstream tasks are running or passed. Inspect the graph, stop running descendants, and reopen passed descendants from leaves toward the target before retrying the upstream reopen.
- After a definition or graph edit, do not automatically rerun or pass implementation tasks. Do so only when the user or delivery workflow requests it and completion evidence exists.

Dependency syntax is always **target first, prerequisites after it**:

```bash
relo task dependency add TASK-003 TASK-001 TASK-002 \
  --reason "TASK-003 requires both inputs"
```

Here `TASK-003` waits for `TASK-001` and `TASK-002`. Every edge needs a non-empty reason. Additions reject missing tasks, duplicates, self-dependencies, and cycles.

Priority is a non-negative sort key; lower numbers appear first. It does not create an edge, unblock a task, or override lifecycle rules.

## Apply milestone semantics without adding strategy

This guide explains milestone mechanics but does not decide whether delivery needs a checkpoint. Keep these three facts distinct:

1. A planned milestone is ready when all of its anchor tasks have passed.
2. Marking revalidates every anchor plus its transitive dependency scope and stores a snapshot.
3. Milestones are non-gating: they never change task dependencies, `task ready`, or `task start` legality.

Only planned milestones are mutable or deletable. Marked milestones are immutable historical records, and later task changes do not rewrite their snapshots. If the user explicitly requests a later revised checkpoint, preserve the marked record and use a new planned milestone rather than probing immutable mutations.

Human tree graphs show active milestone anchors by default. `--all-milestones` adds marked history and `--tasks-only` removes the overlay. These are tree-display options only; graph JSON remains a task-only DAG.

## Handle output and errors correctly

Use human output for interactive work. Use JSON only on the commands explicitly listed in the command reference; do not assume a global `--json` flag.

Successful structured reads use this envelope:

```json
{"schemaVersion":"relo.output/v1","ok":true,"data":{}}
```

For scripts, check both the process exit code and the envelope. Recognized JSON-mode failures return a non-zero exit code with `ok: false` on stdout. Unknown command paths can fail during root discovery before JSON mode is established and may write plain text to stderr. Preserve nullable runtime fields rather than coercing absence into false values.

`validate` writes warnings and errors to stderr. It still writes `OK` to stdout when there are warnings but no errors, so scripts must not treat any stderr output as automatic validation failure.

## Troubleshoot through supported reads

- **Unknown command or flag:** compare `relo --version`, root help, and the relevant subcommand help with the reference. Do not substitute a stale spelling.
- **Project not found:** move to the intended root or a descendant and confirm it was initialized through `relo project show`; an empty metadata directory is insufficient.
- **Task cannot start:** inspect `task get`, `task ready --details`, and `graph`; pending does not imply ready.
- **Definition cannot change:** inspect stored state; stop running work or apply the legal downstream-first reopen sequence for passed work.
- **Title lookup fails:** use the canonical task ID, especially when exact titles are duplicated.
- **Upgrade is refused:** if relo identifies package-manager ownership, follow the named manager's command instead of replacing the executable directly.
- **Project removal is refused or incomplete:** close concurrent relo commands, stay at the project root, and follow any recovery-directory instruction in the error; do not retry blindly.
- **JSON parsing fails:** confirm that the command supports JSON and capture stdout, stderr, and exit status separately.

## Report only verified CLI results

Distinguish commands that were executed from commands merely recommended. Report important stdout/stderr, non-zero exits, and the resulting relo state. A successful relo mutation or clean validation proves CLI state only; it does not prove external implementation work, tests, review, or delivery completion.
