---
name: relo-delivery-orchestrator
description: Orchestrate multi-ticket software delivery with the relo CLI, worker subagents, dependency DAGs, and milestone checkpoints. Use whenever a main coding agent must plan a PRD into tickets, dispatch subagents, verify ticket acceptance, react to milestone recommendations, or coordinate review-discovered work. Do not bypass relo state transitions or edit its database directly.
compatibility: Requires a relo executable, Git, and a harness capable of launching subagents.
---

# Relo delivery orchestrator

Use `relo` as the canonical coordination state. The main agent owns the whole DAG and milestone decisions; worker subagents own only assigned ticket implementation.

## Establish the project

1. Locate the supplied `relo` executable and use that exact path consistently.
2. Run `relo init --prd <path> --goal <goal>` if the project is not initialized.
3. Read the PRD yourself. Plan 5–8 independently verifiable tickets unless the user specifies another size.
4. Create every ticket through `relo task create` with a focused objective, observable acceptance criteria, and deliberate priorities. Add useful ticket context with the exact form `relo task note add TASK-ID --text "..."`; run command help before guessing unsupported flag names.
5. Add dependency edges through `relo task dependency add TASK-ID DEP-ID... --reason "..."`. Always use canonical generated IDs such as `TASK-001`; numeric shorthand is invalid. IDs are identity, not execution order.
6. Create at least one milestone at a meaningful cross-ticket integration boundary. Give it explicit anchors and free-form recommendations that address real combined risks (for example architecture boundaries, broader tests, security, persistence, or documentation). A milestone is not a task and never gates `task ready`.
7. Run `relo validate`, `relo graph`, and `relo status` before dispatch.

## Dispatch workers

1. Immediately before every dispatch, run a fresh `relo task ready` and select only IDs in that exact output. Do not rely on an earlier frontier from before another task transition.
2. Start selected IDs with `relo task start` before launching workers.
3. Give each worker the ticket ID, repository path, relo executable path, and this skill path. Require the worker to run `relo task get <ID>` before coding.
4. Tell workers not to change task or milestone state and not to mark milestones. They should report changed files, checks, and risks.
5. Use separate workers for tickets when practical. Avoid parallel edits to overlapping files; DAG legality does not imply file-level safety.
6. If this agent process has no subagent-launching tool, do not implement the ticket yourself and never describe your own `task get` as a handoff. Write the request to `agent-runs/requests/<ticket-or-milestone>.md`, then emit a `HANDOFF_REQUIRED` block containing role (`worker` or `reviewer`), ticket/milestone ID, repository, relo path, skill path, and exact assignment, then stop. The harness will launch the subagent and return its report in a follow-up. This preserves honest role separation in harnesses that prohibit nested agents.
7. Record actual subagent run IDs when available. Preserve every returned report under `agent-runs/results/<run-id>.md`, including assignment, changed files, checks, commit SHA, and risks. Never claim a handoff or review without a real invocation and durable result. Commit these audit records with the delivery report when the user wants an auditable workflow.
8. Review each worker result and diff against the ticket acceptance criteria. Re-run relevant checks yourself. Resolve actual commit identity with `git rev-parse HEAD` before recording it in immutable task summaries. Then use `task pass`, `task fail`, or `task stop`; do not mark work passed solely because a worker claims completion.
9. Commit coherent verified batches. Do not commit generated caches or secrets.

## Handle milestone checkpoints

After every ticket state change, run `relo milestone ready`. When a checkpoint becomes ready:

1. Run `relo milestone get <ID>` and explicitly consider each recommendation in context of the combined diff, architecture, tests, remaining DAG, and execution budget.
2. Use reviewer or read-only sweeper subagents when a recommendation or integration risk warrants it. If a recommendation explicitly asks for an independent review, a main-agent self-review is not equivalent and must not be used to skip it. Recommendations remain advisory in other cases; skipping one is valid when the reason is evidence-based. If nested agents are unavailable, persist and emit `HANDOFF_REQUIRED` for the reviewer and wait for the harness result.
3. If an original ticket failed its own acceptance criteria, reopen that ticket. If review identifies distinct new work, create a new ticket with acceptance criteria and the next monotonic ID.
4. For non-trivial changes proposed by a reviewer/sweeper, create a normal ticket and attempt rather than silently editing across completed tickets.
5. Update downstream dependencies safely. Running work must be stopped before definition/DAG mutation; passed downstream work must be reopened in a legal order. Milestones never bypass task mutation rules.
6. Add new work as a milestone anchor when it must complete before the checkpoint. Confirm the milestone leaves `ready`, complete the work through the normal worker loop, and confirm it becomes ready again.
7. Keep milestone title/reason durable and timeless; do not rewrite the reason to say it is temporarily ready/not-ready because marked definitions become immutable. Record transient decisions in task notes, reviewer artifacts, and the eventual mark summary.
8. Before marking, save exact verification commands and outputs under `agent-runs/verification/` when auditability matters. Reconcile counts and commit SHAs from artifacts rather than memory.
9. Mark only when the checkpoint is stable. The mark summary must say which recommendations were followed or skipped and why, and mention any review-created ticket. When a reviewer run ID exists, store a useful opaque reference such as `reviewer:&61` and preserve its report; a bare run ID in prose is not independently auditable.

## Finish

1. Complete remaining ready tickets through the same worker loop.
2. Run focused tests, the full suite, formatting/type/lint checks appropriate to the project, `relo validate`, and `relo status`.
3. Confirm no running, ready, blocked, or failed tickets remain and all intended milestones are marked.
4. Write a concise delivery report listing tickets, subagent handoffs, milestone recommendation decisions, review-created work, checks, commits, and residual risks.

Never read or write `.relo/relo.db` directly. Use only public CLI commands.
