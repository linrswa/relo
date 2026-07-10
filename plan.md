# relo 實作計畫

## 1. 背景

`relo` 是一個提供給 AI coding agent 使用的 task management CLI。

PRD 內容、task 拆分、執行順序與派工決策仍由 agent 負責；CLI 不嘗試取代 agent 的判斷，而是提供一致的 task 操作介面、資料驗證、DAG 約束、執行狀態與視覺化。

目標流程：

```text
Main agent 產生 prd.md
        ↓
Main agent 閱讀 PRD，透過 relo task ... 建立 task graph
        ↓
relo 驗證 task、dependency、DAG 與 state
        ↓
Main agent 查看 ready frontier，自行選擇一或多個 task
        ↓
Main agent 只將 task ID/title 交給 subagent
        ↓
Subagent 執行 relo task get <ID> 取得完整工作內容
        ↓
Main agent 根據實作結果更新 pass/fail、task 內容或 DAG
```

## 2. 設計原則

### 2.1 CLI 提供結構，不限制 agent 能力

CLI 強制 hard constraints：

- Task 與 dependency 必須存在。
- Dependency graph 不可形成 cycle。
- Task 的 dependencies 未全部 passed 前不可 start。
- State transition 必須合法。
- Running task 不可直接刪除或修改定義。
- 所有 mutation 必須經過 validation 並原子寫入。

Main agent 決定 soft policy：

- 要先執行哪個 ready task。
- 一次啟動幾個 ready tasks。
- Priority 是否符合當下情況。
- 多個 task 是否適合平行執行。
- 是否需要修改 dependency、拆分或重排 task。
- Failed task 應 retry、修改或放棄。

核心原則：

```text
DAG 決定什麼是合法的。
CLI 顯示所有合法選項。
Agent 決定實際怎麼做。
```

### 2.2 CLI command 是 public contract

Agent 不需要知道或直接修改底層 JSON schema。

穩定的 public contract 是：

- CLI commands 與參數。
- Command validation rules。
- State transition rules。
- 預設文字輸出與可選的 JSON 輸出。
- Exit codes 與錯誤訊息。

資料格式依使用者分層：

- Agent-facing input：CLI flags 與 subcommands。
- Agent-facing output：預設 Markdown/text。
- Harness-facing output：可選、版本化的 JSON。
- Internal persistence：`.relo/relo.db` SQLite database。
- Graph output：Tree 或 JSON。

SQLite schema 僅為 private implementation detail，不是 agent contract；agent 不直接讀寫 database。

### 2.3 PRD 由 agent 產生

`relo` 不產生或解析 PRD 語意。

CLI 只記錄：

- PRD 路徑。
- PRD content hash。
- Project goal。

Main agent 閱讀 `prd.md` 後，自行決定 task 內容並透過 CLI 寫入。

## 3. MVP 範圍

### 3.1 包含

- Project 初始化、PRD 關聯與 PRD hash refresh。
- Task create/get/list/update/delete。
- Acceptance criteria add/update/remove。
- Notes add/update/remove。
- Dependency add/remove。
- Priority update。
- DAG cycle detection。
- Ready、blocked 與 topological layer 計算。
- 多個 ready tasks 的 atomic start。
- Pending/running/passed/failed state 管理。
- Stop、retry 與 reopen。
- Tree-like DAG 與 JSON graph 輸出。
- Private SQLite store、transaction、foreign keys 與 schema migration。
- 適合 agent 閱讀的 `task get` 輸出。

### 3.2 不包含

- PRD 產生器或 PRD parser。
- 自動選擇 task 或 recommendation limit。
- 自動呼叫 harness 的 subagent API。
- Concurrency limit 或固定 wave。
- Worktree isolation。
- Git commit、merge 或 conflict resolution。
- File overlap detection。
- Worker process lifecycle 管理。
- MCP、daemon 或 server。
- 通用 workflow DSL。
- 允許 agent 直接編輯的公開 JSON schema。
- Mermaid graph 與完整 database export/import。

允許多個 task 同時處於 `running`，但 MVP 不負責實際執行環境的隔離。是否安全平行執行，由 main agent 與 harness 判斷。

## 4. Domain model

### 4.1 Project

每個 `.relo/relo.db` 只管理目前 repository 的單一 project，不為未來 multi-project database 預先增加 scope。

```text
Goal
PRD path
PRD hash
Created timestamp
Updated timestamp
Next task sequence
Tasks
```

### 4.2 Task definition

```text
ID                  CLI 產生，例如 TASK-001
Title
Objective
Acceptance criteria
Notes
Dependencies with per-edge reasons
Priority
Creation order
Created timestamp
Updated timestamp
```

### 4.3 Task runtime

```text
Status
Attempt count
Current open attempt（最多一個）
Last failure reason
Last completion summary
Started timestamp
Completed timestamp
```

Task definition 與 runtime 在 domain layer 中應保持概念分離，即使 MVP 儲存在同一個 SQLite database。

### 4.4 Acceptance criterion

```text
ID                  CLI 產生，例如 AC-001
Text
```

Acceptance criterion ID 在單一 task 內唯一。

### 4.5 Note

```text
ID                  CLI 產生，例如 NOTE-001
Text
```

Note ID 在單一 task 內唯一。

### 4.6 Dependency edge

```text
Task ID
Dependency ID
Reason
Created timestamp
```

每一條 dependency edge 保存自己的 reason。Batch add 使用共用 reason 時，CLI 將相同 reason 寫入每一條 edge。

### 4.7 Dependency event

```text
Task ID
Dependency ID
Action              added | removed | reason_updated
Reason
Created timestamp
```

每條被新增、移除或更新 reason 的 edge 都建立獨立 structured event，避免 batch mutation 的 audit 資訊混在單一文字欄位。

### 4.8 Task event

```text
Task ID
Event type          priority_changed | stopped | reopened
Reason
Created timestamp
```

Task event 只記錄需要 reason 的 task-level mutation。Events 是輕量 audit trail，不作為 event-sourced state 的 source of truth。

### 4.9 Attempt

```text
Attempt ID
Task ID
Attempt number
Status              running | passed | failed | interrupted
Started timestamp
Completed timestamp
Summary
Reason
```

同一 task 最多只能有一個 `running` attempt。

## 5. Task state

### 5.1 儲存狀態

```text
pending
running
passed
failed
```

### 5.2 動態狀態

`ready` 與 `blocked` 不直接儲存，每次由 DAG 與目前 state 計算：

```text
ready =
  status == pending
  AND all dependencies are passed

blocked =
  status == pending
  AND at least one dependency is not passed
```

### 5.3 合法 transition

```text
pending → running
running → pending    stop，必須提供 reason
running → passed
running → failed
failed  → pending    retry
passed  → pending    reopen，必須提供 reason
```

### 5.4 Attempt lifecycle

```text
task start
  → attempt_count + 1
  → 建立 status=running 的 attempt
  → task status=running

task pass
  → 關閉目前 open attempt，attempt status=passed
  → task status=passed

task fail
  → 關閉目前 open attempt，attempt status=failed
  → task status=failed

task stop
  → 關閉目前 open attempt，attempt status=interrupted
  → task status=pending
```

`pass/fail/stop` 必須在同一 transaction 中找到並關閉唯一的 open attempt；找不到或找到多個都視為資料一致性錯誤。

### 5.5 修改限制

Pending 或 failed task 可修改：

- Title。
- Objective。
- Acceptance criteria。
- Notes。
- Dependencies。
- Priority。

Running task：

- 不可修改 task definition、dependency 或 priority。
- Main agent 必須先透過 harness 停止對應 subagent。
- 再使用 `relo task stop` 將 task 回到 pending，記錄 interrupted attempt 與 reason。
- 修改完成後可依新的 DAG 重新 start；不需要把正常中止誤標為 failed。

Passed task：

- 不可直接修改。
- 必須先 reopen 並提供 reason。
- 若有 running 或 passed downstream tasks，reopen 必須失敗並列出受影響 task，避免產生不一致狀態。

## 6. CLI command contract

### 6.1 Project

```bash
relo init \
  --prd docs/prd.md \
  --goal "Build task priority support"

relo project show
relo project update --goal "Updated project goal"
relo project update --prd docs/new-prd.md
relo project refresh-prd

relo validate
relo status
relo graph
```

`relo` 應從目前目錄向上尋找 `.relo/`，讓 agent 在 repository 子目錄內也能使用。

`relo init` 在 `.relo/relo.db` 已存在時必須失敗、顯示現有 project 資訊，且 MVP 不提供 `--force`。

`project update --prd` 變更 PRD path 並計算新 hash。當同一路徑的 PRD 內容被修改時，`project refresh-prd` 顯示 old/new hash、記錄 agent 已確認變更，但不自動修改 tasks。

### 6.2 Task CRUD

建立 task：

```bash
relo task create \
  --title "Add priority model" \
  --objective "Persist priority on every task" \
  --accept "Support high, medium and low priorities" \
  --accept "New tasks default to medium priority" \
  --priority 10
```

建立規則：

- `--title` 與 `--objective`/`--objective-file` 必填。
- `--accept` 可重複，但至少需要一個；MVP 不支援 incomplete draft task。
- `--priority` 必須是 `>= 0` 的 integer，預設 `100`，數字越小越優先。

預設輸出建立的 ID：

```text
TASK-001
```

查詢：

```bash
relo task get TASK-001
relo task get --title "Add priority model"
relo task list
relo task list --status running
```

以 title 查詢時使用 exact match；若 title 不唯一，CLI 必須回報歧義並列出匹配的 task IDs。Title lookup 只提供 read command 使用；所有 mutation 只接受 canonical task ID。派工時應優先使用 ID。

更新與刪除：

```bash
relo task update TASK-001 --title "Add task priority enum"
relo task update TASK-001 --objective-file objective.md
relo task update TASK-001 --priority 5 \
  --reason "Infrastructure must be completed before UI work"
relo task delete TASK-001
```

Delete 限制：

- Running task 不可刪除。
- Passed task 不可直接刪除。
- 被 downstream task 依賴時不可刪除，不 cascade 刪除其他 tasks。
- Task-owned acceptance criteria、notes、attempts、events 與它自己建立的 dependency rows，在同一 transaction 內清除。
- MVP 不提供 cascade delete downstream tasks。

### 6.3 Acceptance criteria

```bash
relo task acceptance add TASK-001 \
  --text "Migration succeeds"

relo task acceptance update TASK-001 AC-003 \
  --text "Migration succeeds without data loss"

relo task acceptance remove TASK-001 AC-003
```

Task 永遠至少保留一個 acceptance criterion；移除最後一個 AC 必須失敗。

### 6.4 Notes

```bash
relo task note add TASK-001 \
  --text "Follow the existing Status enum pattern"

relo task note update TASK-001 NOTE-001 \
  --text "Follow the enum pattern in schema.prisma"

relo task note remove TASK-001 NOTE-001
```

### 6.5 Dependencies

單一 dependency：

```bash
relo task dependency add TASK-002 TASK-001 \
  --reason "TASK-002 uses the model created by TASK-001"
```

多個 dependencies 使用共用 reason：

```bash
relo task dependency add \
  TASK-003 \
  TASK-001 TASK-002 TASK-005 \
  --reason "Required before frontend integration"
```

多個 dependencies 使用各自的 reason：

```bash
relo task dependency add \
  TASK-003 \
  TASK-001 TASK-002 TASK-005 \
  --reason-for 'TASK-001=Provides schema' \
  --reason-for 'TASK-002=Provides API' \
  --reason-for 'TASK-005=Provides generated types'
```

`--reason` 與 `--reason-for` mutually exclusive：

- `--reason`：一個共用 reason，複製到本次新增的每一條 edge。
- `--reason-for TASK-ID=TEXT`：以 ID 明確對應 edge，避免依賴 positional order。
- 使用 `--reason-for` 時，每個 positional dependency 必須恰好出現一次，不可遺漏、重複或指定無關 ID。

不採用多個 positional `--reason "reason 1" --reason "reason 2"`，避免 agent 重排 dependency 後 reason 對錯。

Batch remove：

```bash
relo task dependency remove \
  TASK-003 \
  TASK-001 TASK-002 \
  --reason "These dependencies are no longer required"
```

更新既有 edge reason：

```bash
relo task dependency reason \
  TASK-003 TASK-001 \
  --text "Updated dependency rationale"
```

語意為第一個 ID 是 target task，後續 IDs 都是它的 dependencies。Add/remove 都必須在單一 SQLite transaction 中 atomic 執行：

1. 驗證所有 tasks 都存在，輸入 IDs 不可重複。
2. Add 拒絕已存在的 edge；remove 拒絕不存在的 edge。
3. 驗證沒有 self dependency。
4. 對完整暫存結果執行 cycle detection。
5. 驗證 target task 目前允許修改。
6. 為每條 add/remove/reason update 建立 structured dependency event。
7. 全部合法才 commit；任一失敗則 rollback，不能留下 partial mutation。

Dependency reason 顯示於 `task get`。`dependency reason` 只更新 edge metadata，不移除再重建 edge。

Priority 透過 `relo task update --priority` 修改，不提供獨立的 `task priority set`。Priority 必須是 `>= 0` 的 integer，預設 `100`，數字越小越優先；它只影響顯示與預設排序，不限制 main agent 從 ready frontier 中選擇其他 task。

### 6.6 Ready frontier

```bash
relo task ready
relo task ready --details
relo task ready --json
```

CLI 必須回傳所有 ready tasks，不提供 recommendation 或固定 limit。`task list --status` 只接受儲存狀態 `pending|running|passed|failed`；derived ready frontier 只由 `task ready` 提供。

預設排序：

```text
priority → creation order → task ID
```

### 6.7 Runtime state

啟動一或多個 task：

```bash
relo task start TASK-002
relo task start TASK-002 TASK-003 TASK-005
```

Multi-task start 必須是 atomic operation：

1. 驗證所有 task 都存在。
2. 驗證所有 task 都是 ready。
3. 驗證所有 state transitions 合法。
4. 全部通過後才一起改成 running。
5. 任一 task 驗證失敗時，全部保持原狀。

停止、完成與失敗：

```bash
relo task stop TASK-003 \
  --reason "Execution plan changed"

relo task stop TASK-003 TASK-005 \
  --reason "Both tasks require a newly discovered dependency"

relo task pass TASK-002 \
  --summary "Implemented task priority API"

relo task fail TASK-003 \
  --reason "Missing backend dependency"

relo task retry TASK-003

relo task reopen TASK-002 \
  --reason "Implementation requires additional changes"
```

`task stop` 支援一或多個 running tasks，並以單一 transaction atomic 執行。Main agent 必須先停止 harness 中的 workers；CLI 只更新邏輯狀態，將 tasks 回到 pending 並記錄 interrupted attempts。

MVP 建議由 main agent 負責 `start/stop/pass/fail/retry/reopen`。Subagent 只需透過 `task get` 取得工作內容並將執行結果回傳給 main agent。

## 7. Agent 使用流程

### 7.1 從 PRD 建立 task graph

Main agent 自己產生並閱讀 PRD：

```bash
relo init --prd docs/prd.md --goal "Add task priority"

relo task create ...
relo task create ...
relo task create ...

relo task dependency add TASK-002 TASK-001 \
  --reason "Uses the model from TASK-001"
relo task dependency add TASK-003 TASK-001 \
  --reason "Uses the model from TASK-001"
relo task dependency add TASK-004 TASK-002 TASK-003 \
  --reason-for 'TASK-002=Consumes its API' \
  --reason-for 'TASK-003=Reuses its UI component'

relo validate
relo graph
```

Agent 決定 task 內容；CLI 只負責結構與 validation。

### 7.2 選擇與派工

```bash
relo task ready
relo task start TASK-002 TASK-003
```

Main agent 對 subagent 的 prompt 只需要：

```text
Implement TASK-002. Start by running `relo task get TASK-002`.
```

`relo task get TASK-002` 應輸出適合 agent 閱讀的 Markdown，包含：

- Project goal。
- Source PRD。
- Task ID、title、status 與 priority。
- Objective。
- Acceptance criteria checklist。
- Dependencies、per-edge reasons 與其目前狀態。
- Notes。

### 7.3 實作發現原 DAG 錯誤

```bash
# 先透過 harness 停止執行 TASK-003 的 subagent
relo task stop TASK-003 \
  --reason "Discovered a missing API dependency"

relo task dependency add TASK-003 TASK-002 \
  --reason "TASK-003 consumes the API produced by TASK-002"

relo validate
relo graph
```

`TASK-003` 在 `TASK-002` passed 前保持 blocked，之後自動回到 ready frontier。

## 8. DAG 與 tree-like graph

### 8.1 DAG 行為

CLI 必須支援：

- Cycle detection。
- Topological ordering。
- Ready frontier。
- Blocked task 與 unmet dependencies。
- Topological layer。

Layer 計算：

```text
layer(task) = 0                                      if no dependencies
layer(task) = 1 + max(layer(each dependency))        otherwise
```

Layer 只用於顯示，不是執行 barrier。只要一個 task 的直接 dependencies 都 passed，即使較淺層仍有無關 task 未完成，它仍可進入 ready frontier。

### 8.2 預設 tree view

```bash
relo graph
```

預設輸出：

```text
🎯 Goal: Add task priority support
│
├── ✅ TASK-001  Add priority model
│   │
│   ├── 🔵 TASK-003  Add priority API
│   │   │
│   │   └── ⏸ TASK-005  Add priority selector
│   │       ├── also requires: TASK-004
│   │       └── ⏸ TASK-006  Add priority filtering
│   │
│   └── ⏸ TASK-004  Add priority badge
│       ├── also requires: TASK-002
│       └── ↗ TASK-005  already shown
│
└── 🟡 TASK-002  Prepare UI primitives
    └── ↗ TASK-004  already shown

Running: TASK-002
Ready:   TASK-003
Blocked: TASK-004, TASK-005, TASK-006
Failed:  none
```

Deterministic traversal 規則：

1. Goal 是虛擬 root；無 dependency 的 tasks 是 roots。
2. 所有 roots、children 與 `also requires` 都使用相同 comparator：`priority ASC → creation_order ASC → task ID ASC`。
3. 使用 DFS pre-order，依 comparator 順序逐一走訪 roots 與 children。
4. Multi-parent node 第一次被 DFS 走訪時，由該 parent 完整展開；這個 parent 是 deterministic primary display parent。
5. 再次遇到相同 node 時只顯示 `↗ TASK-ID already shown`，不重複展開。
6. 完整展開的 multi-parent node 顯示 primary display parent 以外的所有 direct dependencies，標為 `also requires` 並依 comparator 排序。
7. Tree 最後顯示 running、ready、blocked、failed summary，task IDs 仍依 comparator 排序。

相同 database state 不論 query row order，都必須產生 byte-for-byte 相同的 tree output。

狀態符號：

```text
✅ passed
🟡 running
🔵 ready
⏸ blocked
❌ failed
```

Tree view 是 DAG 的閱讀 projection；validation 永遠使用完整 dependency graph。

### 8.3 其他格式

```bash
relo graph --format tree
relo graph --format json
```

MVP 保留 Tree 與 JSON graph；Mermaid 延後到 post-MVP。

## 9. Validation

### 9.1 每次 mutation

所有 write command 在寫入前必須檢查：

- Project 已初始化。
- Task、acceptance criterion 或 note 存在。
- State transition 合法。
- Dependency reference 有效。
- DAG 無 cycle。
- Running/passed task 是否允許該 mutation。
- Delete 是否有 downstream references。

驗證失敗時：

- 不可產生 partial mutation。
- 回傳非零 exit code。
- 顯示具體原因與可採取的修正方式。

### 9.2 完整驗證

```bash
relo validate
```

Errors：

- Project goal 為空。
- PRD 不存在。
- Task title 或 objective 為空。
- Task 沒有 acceptance criterion。
- Dependency reference 不存在。
- Dependency graph 有 cycle。
- Runtime state 與 dependency 不一致。
- Running task 的 dependency 尚未 passed。

Warnings：

- PRD content hash 已改變。
- Task 沒有 notes。
- Task 有過多 acceptance criteria。
- 多個 tasks 使用相同 priority。
- Title lookup 存在歧義。

Warnings 不阻擋 read operations；是否阻擋 start 由各 warning 類型明確定義。MVP 中只有 errors 阻擋 start。

## 10. Persistence

### 10.1 檔案配置

```text
.relo/
└── relo.db
```

### 10.2 SQLite transaction 規則

- SQLite schema 是 private implementation detail；CLI 是唯一 writer，agent 不直接讀寫 database。
- 啟用 `PRAGMA foreign_keys = ON`、busy timeout 與 WAL mode。
- 使用 `PRAGMA user_version` 管理 internal schema migration。
- 所有 write command 使用 `BEGIN IMMEDIATE` 或 driver 的等效 write transaction。
- Write transaction 必須先取得 writer lock，再從 transaction 內重新讀取最新 graph/state、執行 validation、mutation，最後 commit。
- Busy retry 必須重跑整個 transaction closure，包括重新讀取與 validation；不可沿用 lock 前的 snapshot 或計算結果。
- Batch dependency add/remove、multi-task start/stop 必須在單一 transaction 內完成。
- Cycle detection 可在 Go service layer 計算，但必須讀取 transaction 內的最新 graph，並在同一 transaction commit 前完成。
- CLI crash、busy、constraint error 或 validation failure 不可留下 partial mutation。

MVP database 只管理一個 project。Logical DDL constraints：

```text
projects
  id INTEGER PRIMARY KEY CHECK (id = 1)
  goal TEXT NOT NULL CHECK (goal <> '')
  prd_path TEXT NOT NULL CHECK (prd_path <> '')
  prd_hash TEXT NOT NULL CHECK (prd_hash <> '')
  next_task_sequence INTEGER NOT NULL CHECK (next_task_sequence > 0)
  created_at TEXT NOT NULL
  updated_at TEXT NOT NULL

tasks
  id TEXT PRIMARY KEY
  title TEXT NOT NULL CHECK (title <> '')
  objective TEXT NOT NULL CHECK (objective <> '')
  priority INTEGER NOT NULL DEFAULT 100 CHECK (priority >= 0)
  creation_order INTEGER NOT NULL UNIQUE
  status TEXT NOT NULL CHECK (status IN ('pending','running','passed','failed'))
  attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0)
  last_failure_reason TEXT
  last_completion_summary TEXT
  created_at TEXT NOT NULL
  updated_at TEXT NOT NULL

acceptance_criteria
  task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE
  criterion_id TEXT NOT NULL
  text TEXT NOT NULL CHECK (text <> '')
  position INTEGER NOT NULL CHECK (position >= 0)
  PRIMARY KEY (task_id, criterion_id)
  UNIQUE (task_id, position)

notes
  task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE
  note_id TEXT NOT NULL
  text TEXT NOT NULL CHECK (text <> '')
  position INTEGER NOT NULL CHECK (position >= 0)
  PRIMARY KEY (task_id, note_id)
  UNIQUE (task_id, position)

dependencies
  task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE
  dependency_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT
  reason TEXT NOT NULL CHECK (reason <> '')
  created_at TEXT NOT NULL
  PRIMARY KEY (task_id, dependency_id)
  CHECK (task_id <> dependency_id)

attempts
  id INTEGER PRIMARY KEY
  task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE
  attempt_number INTEGER NOT NULL CHECK (attempt_number > 0)
  status TEXT NOT NULL CHECK (status IN ('running','passed','failed','interrupted'))
  started_at TEXT NOT NULL
  completed_at TEXT
  summary TEXT
  reason TEXT
  UNIQUE (task_id, attempt_number)

dependency_events
  id INTEGER PRIMARY KEY
  task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE
  dependency_id TEXT NOT NULL
  action TEXT NOT NULL CHECK (action IN ('added','removed','reason_updated'))
  reason TEXT NOT NULL CHECK (reason <> '')
  created_at TEXT NOT NULL

task_events
  id INTEGER PRIMARY KEY
  task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE
  event_type TEXT NOT NULL CHECK (event_type IN ('priority_changed','stopped','reopened'))
  reason TEXT NOT NULL CHECK (reason <> '')
  created_at TEXT NOT NULL
```

另外建立 partial unique index，保證每個 task 最多一個 open attempt：

```sql
CREATE UNIQUE INDEX one_running_attempt_per_task
ON attempts(task_id)
WHERE status = 'running';
```

Delete task 時，若有其他 row 以 `dependency_id` 指向它，`ON DELETE RESTRICT` 阻擋；task-owned AC、notes、attempts、events 與 `task_id` 方向的 dependency rows 使用 `ON DELETE CASCADE` 清理。

### 10.3 JSON output contract

預設 `task get` 使用 Markdown/text；`--json` 提供給 harness。JSON 是版本化 output contract，不是 internal persistence format。

所有 JSON command 使用統一 envelope：

```json
{
  "schemaVersion": "relo.output/v1",
  "ok": true,
  "data": {}
}
```

錯誤：

```json
{
  "schemaVersion": "relo.output/v1",
  "ok": false,
  "error": {
    "code": "TASK_NOT_READY",
    "message": "TASK-003 is blocked",
    "details": {}
  }
}
```

Output 規則：

- Human-readable success：stdout。
- Human-readable error/diagnostic：stderr。
- `--json` success/error envelope：stdout；error 同時回傳非零 exit code。
- Exit `0`：成功，可能包含 warnings。
- Exit `1`：I/O、database busy exhausted 或其他 operational error。
- Exit `2`：validation、invalid argument、constraint 或 state transition error。

MVP 不提供 database export/import。

## 11. Go architecture

```text
cmd/
└── relo/
    └── main.go

internal/
├── domain/
│   ├── project.go
│   ├── task.go
│   └── state.go
├── store/
│   ├── store.go
│   ├── sqlite.go
│   ├── migrations.go
│   └── transaction.go
├── task/
│   ├── service.go
│   ├── validation.go
│   └── transitions.go
├── dag/
│   ├── graph.go
│   ├── cycle.go
│   ├── ready.go
│   └── layers.go
├── render/
│   ├── tree.go
│   └── json.go
└── cli/
    ├── root.go
    ├── project.go
    ├── task.go
    └── graph.go

testdata/
├── valid/
├── cycles/
├── multi-parent/
└── runtime/
```

建議依賴：

- Cobra：nested commands 與一致的 help/output。
- Pure-Go SQLite driver：避免 CGO 影響單一 binary 與跨平台發布；實作前先以 cross-compile spike 確認 driver。
- 其餘優先使用 Go standard library。

Domain、DAG 與 task service 不應依賴 Cobra，確保核心邏輯可以獨立測試。

## 12. 實作工作 DAG

| ID | 工作 | Depends on |
|---|---|---|
| TASK-001 | Bootstrap Go CLI and define domain contract | — |
| TASK-002 | Implement constrained SQLite schema, migrations, and safe write transactions | TASK-001 |
| TASK-003 | Implement project initialization, repeat-init guard, and root discovery | TASK-002 |
| TASK-004 | Implement basic task CRUD, AC requirement, priority, and delete semantics | TASK-003 |
| TASK-005 | Implement acceptance criteria and notes CRUD | TASK-004 |
| TASK-006 | Implement atomic batch dependencies, structured reasons/events, reason update, and cycle detection | TASK-004 |
| TASK-007 | Implement full validation, topological layers, ready and blocked calculation | TASK-005, TASK-006 |
| TASK-008 | Implement runtime state transitions and attempt lifecycle | TASK-007 |
| TASK-009 | Implement atomic multi-task start and stop | TASK-008 |
| TASK-010 | Implement deterministic tree and JSON graph rendering | TASK-008 |
| TASK-011 | Implement status views and versioned JSON output contract | TASK-009, TASK-010 |
| TASK-012 | Add agent workflow documentation and end-to-end tests | TASK-011 |

Tree-like view：

```text
🎯 Implement relo MVP
│
└── TASK-001  CLI/domain
    └── TASK-002  SQLite store/transactions
        └── TASK-003  Project init
            └── TASK-004  Task CRUD
                ├── TASK-005  Acceptance/notes CRUD
                │   └── TASK-007  Validation/ready/layers
                │       └── TASK-008  Runtime/attempt lifecycle
                │           ├── TASK-009  Atomic multi-start/stop
                │           │   └── TASK-011  Status/JSON output
                │           │       └── TASK-012  Agent workflow/E2E
                │           └── TASK-010  Deterministic tree/JSON graph
                │               └── ↗ TASK-011  also requires TASK-009
                │
                └── TASK-006  Dependency DAG/batch reasons
                    └── ↗ TASK-007  also requires TASK-005
```

## 13. Milestones

### Milestone 1：Task management foundation

包含 TASK-001～TASK-004：

- Go module 與 CLI skeleton。
- Domain model。
- Private SQLite store、migrations、foreign keys 與 transactions。
- `relo init` 與 project discovery。
- Task create/get/list/update/delete。

完成後先確認 CLI 操作手感與 task model，再繼續擴充。

### Milestone 2：Task structure and DAG

包含 TASK-005～TASK-007：

- Acceptance criteria 與 notes CRUD。
- Atomic batch dependency add/remove。
- Shared與ID-keyed per-edge reasons。
- Priority mutation。
- Cycle detection。
- Full validation。
- Ready、blocked、topological layers。

### Milestone 3：Runtime and agent-controlled concurrency

包含 TASK-008～TASK-009：

- Pending/running/passed/failed transitions。
- Stop、retry 與 reopen。
- 多個 ready tasks 的 atomic start/stop。
- 不設定 concurrency limit。

### Milestone 4：Visualization and integration UX

包含 TASK-010～TASK-012：

- Deterministic tree-like DAG。
- JSON graph。
- Versioned JSON output envelope 與 status summary。
- Agent workflow 文件。
- End-to-end tests。

## 14. Testing strategy

### 14.1 Unit tests

Domain：

- Task、AC、note ID generation。
- State transitions。
- Mutation restrictions。

DAG：

- Empty graph。
- Single root。
- Multiple roots。
- Diamond dependency。
- Multi-parent graph。
- Self dependency。
- Direct與indirect cycle。
- Stable topological ordering。
- Layer calculation。
- Ready/blocked calculation。

Rendering：

- Stable comparator、DFS pre-order 與 root ordering。
- Tree expansion。
- Repeated node references。
- `also requires` annotations 與 ordering。
- State symbols。
- 不同 DB insertion/query row order 產生 byte-for-byte 相同 tree 的 golden tests。

### 14.2 Store tests

- Init/read/write round trip。
- Migration 與 `PRAGMA user_version`。
- Foreign key constraints。
- Transaction commit/rollback。
- Busy timeout、WAL 與 concurrent readers/writer。
- `BEGIN IMMEDIATE` 後重新讀取/validation。
- Busy retry 重跑完整 transaction closure。
- Concurrent dependency writers 不可產生 cycle。
- Concurrent start writers 不可重複啟動同一 task。
- Batch dependency mutation 的 all-or-nothing 行為。
- Multi-task start/stop 的 all-or-nothing 行為。
- Failed validation 不修改 database。

### 14.3 CLI integration tests

- Init → reject repeated init → project refresh-prd → create → update → get → delete。
- Reject task create without AC and invalid priority。
- Build valid DAG and show per-edge reasons in `task get`。
- Add/remove multiple dependencies atomically and update edge reason without rebuilding it。
- Validate shared `--reason` and ID-keyed `--reason-for`。
- Reject cycle mutation without partial edges。
- Return full ready frontier。
- Atomic start/stop multiple ready/running tasks。
- Reject mixed valid/invalid multi-start without partial updates。
- Pass dependencies and unlock downstream task。
- Stop → modify DAG → start。
- Fail → retry。
- Reopen safety with downstream tasks。
- Task lookup by ID and title ambiguity handling；mutation rejects title。
- Attempt lifecycle and one-open-attempt constraint。
- JSON success/error envelope、stdout/stderr 與 exit codes。

### 14.4 End-to-end agent scenario

模擬完整流程：

1. 已存在 agent-generated PRD。
2. 只用 CLI 建立 task graph。
3. Validate 並輸出 tree。
4. 啟動兩個 independent tasks。
5. 完成其中一個，因發現缺少 dependency 而 stop 另一個。
6. 修改 stopped task 的 dependency。
7. Dependency 完成後重新 start，並完成所有 tasks。
8. 全流程不直接讀寫 `.relo/relo.db`。

## 15. Definition of Done

MVP 只有在以下條件成立時才算完成：

- Agent 不需要知道或編輯 task JSON schema。
- Agent 可完全透過 CLI 從 PRD 建立 task graph。
- Subagent 只靠 task ID/title 能取得完整工作內容。
- Task create 強制至少一個 AC；priority 為 `>=0` integer、預設 100、數字越小越優先。
- Batch dependency add/remove 支援共用 reason 或 ID-keyed per-edge reasons，且 edge reason 可獨立更新。
- Cycle mutation 被拒絕且不產生 partial edges。
- `task ready` 回傳完整 ready frontier。
- Main agent 可選擇任意一或多個 ready tasks。
- Attempt lifecycle 完整，且每個 task 最多一個 open attempt。
- Multi-task start/stop 為 atomic，且不設定 concurrency limit。
- Main agent 可正常中止 running task、修改 dependency、priority 或內容後重新執行。
- Tree graph 能清楚且 deterministic 地呈現 root、階層、multi-parent 與重複引用。
- Process restart 後 task definition 與 runtime state 完整保留。
- Concurrent CLI mutations 使用 lock-first read/validate/write transaction，不會因 stale snapshot 破壞 DAG/state。
- 所有 JSON output 使用 `relo.output/v1` envelope；validation error 都有具體訊息與非零 exit code。
- 完整 E2E scenario 不需要直接操作 `.relo/relo.db`。

## 16. 後續可能方向

以下只記錄為未來方向，不納入 MVP：

- Worker `report` 與 main agent `approve/reject`。
- Worktree isolation 與 Git integration。
- File overlap advisory warnings。
- Harness adapters。
- Crash recovery 與 abandoned running task reconciliation。
- 完整 event sourcing、history query 與 audit UI。
- Mermaid graph。
- Database export/import。
- Dynamic task split/supersede commands。
- Workflow recipes。
- MCP 或 long-running daemon。
