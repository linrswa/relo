# End-to-End Agent Scenario

This scenario is the canonical MVP flow for agent-driven work. It must be executed entirely through the `relo` CLI; agents must not directly read or write `.relo/relo.db`.

## Exact scenario

1. 已存在 agent-generated PRD。
2. 只用 CLI 建立 task graph。
3. Validate 並輸出 tree。
4. 啟動兩個 independent tasks。
5. 完成其中一個，因發現缺少 dependency 而 stop 另一個。
6. 修改 stopped task 的 dependency。
7. Dependency 完成後重新 start，並完成所有 tasks。
8. 全流程不直接讀寫 `.relo/relo.db`。

## Expected invariants

- `init` points at the existing PRD path and records the project goal.
- All tasks, acceptance criteria, notes, and dependencies are created or changed with CLI commands.
- `validate` succeeds before dispatch and after graph changes.
- `graph --format tree` shows a deterministic task tree and status summary.
- `task ready` exposes the complete ready frontier before each start.
- Starting multiple tasks is atomic and uses task IDs only.
- Stopped work returns to pending, can receive a new dependency, and is blocked until that dependency passes.
- Final `status --json` uses the `relo.output/v1` envelope and has no running, ready, blocked, or failed tasks after all tasks pass.
