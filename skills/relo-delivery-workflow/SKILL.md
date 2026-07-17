---
name: relo-delivery-workflow
description: Plan and carry out non-trivial software delivery with the relo CLI. Use whenever the user asks to turn an existing PRD into an executable task DAG, resume an initialized relo project, coordinate ready work, verify task completion, revise delivery dependencies, or manage integration checkpoints. During initial DAG planning, proactively evaluate whether meaningful milestone checkpoints are needed even when the user does not explicitly mention milestones. This skill defines delivery workflow and planning policy; use relo-cli-guide when available, or command help, for exhaustive CLI syntax.
compatibility: Requires access to a relo executable and a repository containing, or ready to initialize from, an existing PRD.
---

# Relo delivery workflow

Use `relo` as the canonical delivery state while keeping implementation decisions with the coding agent. This workflow is harness-neutral: use subagents, worktrees, or other isolation only when the user requests them or the available environment makes them useful.

## Operating boundaries

1. Use only public `relo` commands. Never read, edit, query, or otherwise depend on `.relo/relo.db` directly.
2. Locate the executable and inspect relevant command help instead of guessing syntax. If `relo-cli-guide` is available, consult it for the complete command contract.
3. Treat task IDs as identity and the DAG as execution legality. Priority and numeric ID order do not replace dependencies.
4. Keep workflow proportional to the work. Do not force multiple tasks, milestones, workers, commits, or audit artifacts onto a trivial change.
5. Do not claim implementation, verification, handoff, or review that did not occur. A relo state transition records actual work; it is not a substitute for doing or checking that work.
6. Respect explicit user choices. If the user excludes milestones, parallel work, subagents, or another workflow element, do not add it implicitly.

## Establish project context

Before planning or resuming delivery:

1. Read the PRD, relevant repository instructions, and enough existing code and tests to understand the requested outcome.
2. Locate the executable with `command -v relo` or use the exact path supplied by the user. Run `relo --help` and relevant subcommand help when needed.
3. Determine whether the repository is already initialized. An empty `.relo/` directory is not sufficient; project discovery requires `.relo/relo.db`.
4. For an existing project, inspect before mutating:

```bash
relo project show
relo status
relo graph
relo validate
```

   Human graph output already includes active milestone anchors. Use `relo graph --all-milestones` only when marked history matters, and always treat checkpoint overlays as informational rather than task-DAG legality.

5. For a new project, confirm that the PRD exists and the goal is clear, then initialize it:

```bash
relo init --prd PATH --goal "PROJECT GOAL"
```

Do not reinitialize or replace existing state to make planning easier. Reconcile the requested work with the current project instead.

## Build an executable task DAG

Translate the requested outcome into the smallest useful set of independently verifiable tasks:

- Give each task one focused objective and at least one observable acceptance criterion.
- Include implementation context in notes rather than hiding additional deliverables in the title.
- Add a dependency only when the target cannot be correctly completed before that prerequisite passes.
- Preserve real parallelism. Do not serialize tasks merely because that is the order in which they were written.
- Use priority to express selection preference among otherwise legal work; lower numbers sort first.
- Avoid both oversized tasks that cannot be verified independently and tiny tasks whose state-management overhead exceeds their value.

Treat verification according to whether it is a completion condition or an independent work package:

- Keep focused tests with the implementation task when they exercise that task's local behavior, use the same code boundary and owner, require no substantial new harness or environment, and must pass before the implementation can honestly be considered complete. Express them as acceptance criteria.
- Do not hide substantial verification work inside another task's acceptance criteria. Create a separate verification task when it requires meaningful harness, fixtures, environment, CI, cross-component coordination, performance or security methodology, migration or compatibility analysis, or other independently assignable and reviewable work.
- Give a separate verification task its own objective, dependencies, and acceptance criteria. A milestone may anchor that task to trigger combined review, but the milestone does not replace the verification work.

Create tasks and dependencies through the CLI, preserve every generated canonical ID, then verify the plan:

```bash
relo validate
relo graph
relo task ready --details
```

If validation or the rendered graph contradicts the intended plan, fix the task definitions or edges before starting work.

## Plan milestone checkpoints deliberately

Milestones are optional in the data model, but **milestone evaluation is part of initial delivery planning**. Do not interpret “optional” as permission to ignore checkpoints without considering delivery risk.

### Prefer a milestone when

Create a planned milestone at a meaningful integration boundary when one or more of these conditions applies:

- several tasks converge into a shared integration point;
- schema, API, storage, protocol, migration, or compatibility boundaries must agree;
- frontend and backend, multiple packages, or multiple services must work together;
- authentication, authorization, security, persistence, or release behavior deserves a combined check;
- a foundation will be consumed by substantial downstream work and should be reviewed first;
- the DAG is long enough that an intermediate stability checkpoint reduces rework;
- broader tests or architectural review are valuable only after a set of tasks is complete.

A non-trivial multi-task delivery with a genuine integration boundary should normally have at least one milestone even if the user did not mention milestones.

### Skip a milestone when

It is reasonable to omit milestones when the work is a single task, a small low-risk change, or a short plan where a checkpoint would merely repeat task acceptance criteria. Also omit them when the user explicitly declines them. State the reason briefly in the planning report so omission is a deliberate decision rather than an oversight.

Do not add a retroactive milestone to nearly finished work unless it would still cause a useful combined review.

### Choose useful anchors and recommendations

- Anchors describe the completion boundary; transitive dependencies are included in scope automatically.
- Do not make every scope task an anchor when one downstream integration task already represents the boundary.
- Keep milestone titles and reasons durable rather than describing temporary readiness.
- Recommendations should address combined risk, such as an integration suite, migration review, security review, or cross-component behavior. Do not merely repeat one task's acceptance criteria.
- Milestones are non-gating. Never use one as a substitute for a dependency edge or task acceptance criterion.

After creating planned milestones, inspect the resulting project again with `relo milestone list`, `relo graph`, and `relo validate`.

## Execute the ready-work loop

Continue until the requested delivery scope is complete:

1. Fetch a fresh ready frontier immediately before selecting work:

```bash
relo task ready --details
```

2. Select only ready tasks. Consider dependency legality, priority, file overlap, and available execution capacity.
3. Start selected IDs before implementation. Multi-task start is atomic, but relo does not provide process or file isolation.
4. Execute the work using the current harness. Subagents are optional; when used, give them canonical task IDs and require them to inspect the task details before coding.
5. Verify the implementation against every acceptance criterion and inspect the actual diff or output.
6. Pass a task only after verification. Record a concise evidence-based summary.
7. If work is invalid or blocked, use `fail` or `stop` with an accurate reason, then recover through legal transitions.
8. Refresh status and milestone readiness after meaningful state changes.

Do not mark a task passed solely because an implementation agent reports success. The agent controlling relo state remains responsible for checking the evidence available to it.

## Revise tasks and dependencies safely

When implementation reveals an incorrect objective, missing dependency, or new work:

- Stop running work before changing its definition or dependency edges.
- Retry failed work before starting it again.
- To revise passed upstream work, inspect downstream state first. Stop running descendants, then reopen passed descendants from leaves toward the upstream task before reopening it.
- Represent distinct non-trivial work as a normal new task with acceptance criteria instead of silently expanding an unrelated completed task.
- Preserve monotonic IDs; do not reinterpret a new ID as an execution-order requirement.
- After every structural revision, rerun:

```bash
relo validate
relo graph
relo task ready --details
```

Resume affected work in dependency order.

## Review ready milestones

After task completions and structural changes, check:

```bash
relo milestone ready --details
```

A ready milestone means its anchors passed and review may begin; it does **not** mean the checkpoint should be marked automatically.

For each ready milestone:

1. Inspect its anchors, transitive scope, and recommendations with `relo milestone get MILESTONE-ID`.
2. Review the combined diff, architecture boundaries, integration behavior, test coverage, remaining DAG, and execution budget.
3. Follow, replace, or skip each recommendation based on evidence. Recommendations are advisory, but ignoring them without consideration defeats the checkpoint.
4. If an original task missed its own acceptance criteria, reopen that task through legal state transitions.
5. If review discovers distinct work, create a new task. Add it as an anchor when it must pass before this checkpoint is stable; the milestone should then leave the ready set until that work passes.
6. Mark only after the complete scope is passed and the checkpoint is sufficiently stable. Summarize the checks performed and any recommendations followed, replaced, or skipped.

Marked milestones are immutable historical snapshots. Preserve them and create a successor milestone when a later checkpoint is needed.

## Finish delivery

Before declaring completion:

1. Run the narrowest relevant checks, then the broader test, format, lint, type, build, or security checks justified by the project.
2. Run `relo validate` and inspect `relo status`.
3. Confirm every intended task is passed and no unexpected running, ready, blocked, or failed work remains.
4. Resolve every planned milestone in scope: mark it after review, or explicitly explain why it remains planned.
5. Report completed tasks, meaningful DAG changes, milestone decisions, verification evidence, and residual risks.

Completion means both the implementation and relo state match the requested outcome. A clean relo status alone does not prove the external implementation is correct.

## Avoid these anti-patterns

- Skipping milestone planning merely because milestones are optional.
- Creating a milestone for every task or adding checkpoints with no integration decision to make.
- Treating a milestone as a dispatch barrier or dependency edge.
- Marking immediately when anchors pass without reviewing the complete scope.
- Replacing task acceptance criteria with milestone recommendations.
- Hiding substantial integration, performance, security, migration, compatibility, environment, or test-harness work inside an implementation acceptance criterion.
- Mechanically splitting every focused unit test into a separate task when it has no independent delivery value.
- Forcing subagents or pretending a handoff occurred when the harness does not support one.
- Mutating state as a probe, bypassing rejected transitions, or touching the SQLite database directly.
- Passing tasks or marking milestones based on intention rather than verified work.
