# relo Milestone Checkpoint 實作計畫

## 1. 背景

目前 `relo` 以 Task、dependency DAG 與 runtime state 管理可交付工作。每個 worker 在完成單一 task 時，依 acceptance criteria 驗證交付；main agent 再根據結果執行 `pass`、`fail`、`stop`、`retry` 或 `reopen`。

單一 task 驗證無法完全涵蓋跨 task 問題，例如：

- 多個 task 合併後的架構一致性。
- 重複程式碼或抽象邊界漂移。
- Integration test、security review 或文件完整性。
- 後續工作開始前是否值得插入 refactor。
- 原本 DAG 是否需要依最新發現重新排序。

本功能加入一個不屬於 Task、也不改變 DAG 合法性的 Milestone checkpoint。Milestone 類似 Git tag：它描述 task graph 上一個值得停下來思考的具名切面；main agent 可在此自行決定是否 review、執行 read-only sweeper、補測試、建立新 task、修改 DAG，或直接標記 checkpoint 完成。

Milestone 不負責執行 worker、不自動觸發 reviewer，也不管理 Git、worktree、container 或 harness。

## 2. 目標

- Agent 可自行決定哪些 DAG 切面值得建立 Milestone。
- Milestone 不成為 Task，不佔用 task runtime state 或 attempt。
- Milestone 不建立 phase barrier，不阻止任何 ready task。
- 使用 anchor tasks 定義 checkpoint scope，避免要求每個 task 固定屬於某個 phase。
- Anchor tasks 全部 passed 時，Milestone 動態成為 `ready_to_mark`。
- Main agent 可在 `ready_to_mark` 時根據自由文字 recommendations 自主決定下一步。
- Review 發現新工作時，可建立新 task、修改 DAG、加入新 anchor；既有 Task IDs 永遠不重新編號。
- `mark` 時以單一 transaction 捕捉 immutable task snapshot。
- Process restart 後 Milestone、recommendations 與 snapshot 完整保留。
- CLI 保持唯一 public writer；agent 不需要知道 SQLite schema。

## 3. 非目標

本次不包含：

- 自動啟動 reviewer、sweeper 或其他 subagent。
- Recommendation enum、workflow DSL 或固定 check types。
- Recommendation completion/outcome state machine。
- Milestone 對 task dispatch 的 hard gate。
- 固定 phase、wave 或 concurrency limit。
- 自動選擇平行 tasks。
- Git commit、tag、branch、merge 或 worktree 操作。
- Container、`.venv`、dependency cache 或 workspace pool 管理。
- Harness-specific worker lifecycle。
- 自動從 review finding 產生 task。
- Marked Milestone 的 reopen、rewrite、delete 或 supersede。
- Milestone 間的 dependency graph。
- 自動分析 marked snapshot 與目前 task state 的 drift。

可選的 `reference` 只是由 harness/main agent 提供的 opaque string；`relo` 不解讀、不驗證，也不對它執行任何外部操作。

## 4. 核心設計原則

### 4.1 Task、Phase 與 Milestone 分離

```text
Task       = 要執行且可驗收的工作
Phase      = 排程分組或 barrier（本功能不引入）
Milestone  = 不執行的 checkpoint marker
```

Milestone 不應改變：

- Task 的 ready/blocked 計算。
- Task dependency 合法性。
- `task ready` 回傳內容。
- Main agent 從 ready frontier 選擇任意 task 的能力。

### 4.2 DAG 決定合法性，Milestone 提醒 agent 思考

```text
DAG 決定什麼可以開始。
Milestone 顯示何時形成值得檢查的 checkpoint。
Recommendation 提供可能的行動方向。
Agent 決定是否 review、sweep、補測試、改 DAG 或直接 mark。
```

### 4.3 Task ID 是 identity，不是執行順序

新增 task 永遠使用下一個單調遞增 ID。即使 review 後把新 task 邏輯上插入兩個既有 tasks 中間，也不得重新編號或重用 ID。

```text
原 DAG：
TASK-001 → TASK-002 → TASK-003 → TASK-004

插入 refactor 後：
TASK-001 → TASK-002 → TASK-003 → TASK-005 → TASK-004
```

真正的執行順序由 dependencies 決定；priority 只影響預設排序；creation order 只記錄歷史建立順序。

## 5. Domain model

### 5.1 Milestone

```text
ID                       CLI 產生，例如 MILESTONE-001
Title
Reason
Stored status            planned | marked
Anchors
Recommendations
Next recommendation seq
Mark summary             marked 時必填
Reference                optional opaque string
Created timestamp
Updated timestamp
Marked timestamp
```

### 5.2 Milestone anchor

```text
Milestone ID
Task ID
Created timestamp
```

Anchor 是 agent 選定的 checkpoint boundary point。Milestone scope 由 anchors 與其完整 transitive dependency closure 動態計算。

Anchors 不要求形成 graph antichain。若 TASK-007 depends on TASK-006，兩者可同時保留為 anchors；TASK-006 雖然已在 TASK-007 closure 中，仍表達 agent 明確選定的 checkpoint 意圖。Scope 計算必須去重，ready 判斷仍要求每個明列 anchor passed。

同一個 task：

- 可作為多個 milestones 的 anchor。
- 不需要固定歸屬某個 milestone。
- 不因 milestone 而改變 task state 或 dependency。
- 只在 milestone planned 期間保存為 live foreign-key reference；mark 後 anchor identity 移入 snapshot，live anchor rows 在同一 transaction 刪除。

### 5.3 Recommendation

Recommendation 是自由文字建議，不是 enum 或必做 check。

```text
ID                       單一 milestone 內唯一，例如 REC-001
Text
Position
Created timestamp
Updated timestamp
```

範例：

```text
Review cross-task integration boundaries
Consider running a read-only refactor sweeper
Run the full regression suite
Check whether the next tasks should be reordered
```

Recommendation：

- 可為任意非空字串。
- 不儲存 pending/completed/skipped。
- 不自動觸發任何工具或 agent。
- 不阻擋 `mark`。
- 是否採納與如何執行，由 main agent 自行判斷。

Agent 最後可在 mark summary 中記錄採用或跳過 recommendations 的理由。

### 5.4 Mark snapshot

`milestone mark` 時，為 scope 中每個 task 保存 immutable snapshot：

```text
Milestone ID
Task ID                  保存文字 identity，不依賴 task row 長期存在
Task title
Priority                 mark 時的排序資料
Creation order           mark 時的排序資料
Scope position           依 comparator 計算的 immutable output order
Is anchor                是否為 mark 時明列 anchor
Attempt number
Status                   mark 時必須為 passed
Completion summary
Task updated timestamp
Captured timestamp
```

Snapshot 是 marked milestone 的唯一 scope/anchor rendering source。Milestone marked 後，即使 task 日後被 reopen、改 priority、修改定義或合法刪除，`milestone get` 都只讀 snapshot，不查詢 mutable task rows，既有輸出順序與內容不修改。

## 6. Scope 與 derived state

### 6.1 Scope

```text
scope(milestone) =
  anchors
  UNION every transitive dependency reachable from anchors
```

計算必須：

- Planned milestone 使用單一 read transaction 內的完整 dependency graph。
- 對 diamond/multi-parent DAG 與 redundant anchors 去重。
- 使用現有 deterministic comparator 排序：`priority → creation order → task ID`。
- 在 graph corruption 或 cycle 時回傳 validation error。
- Marked milestone 不重新計算 live closure；scope 與 anchors 完全來自 immutable snapshot。

### 6.2 Ready to mark

`ready_to_mark` 不直接儲存：

```text
ready_to_mark =
  milestone.status == planned
  AND milestone has at least one anchor
  AND every anchor status == passed
```

在正常 runtime invariant 下，passed anchor 的 dependency closure 也應全部 passed。`mark` transaction 仍必須重新驗證完整 scope，避免 corrupted database 或 concurrent mutation 產生不一致 snapshot。

### 6.3 Marked

```text
marked = stored status == marked
```

Marked Milestone immutable：

- 不可修改 title、reason。
- 不可增減 anchors。
- 不可增刪改 recommendations。
- 不可重寫 mark summary、reference 或 snapshot。
- MVP 不提供 reopen、delete 或 force rewrite。

## 7. Agent workflow

### 7.1 建立 planned Milestone

```bash
relo milestone create \
  --title "Storage foundation stable" \
  --reason "Backend foundation is complete enough for integration review" \
  --anchor TASK-005 \
  --anchor TASK-006 \
  --recommend "Review cross-task integration boundaries" \
  --recommend "Consider running a read-only refactor sweeper" \
  --recommend "Run the full regression suite"
```

### 7.2 Anchors passed

當 TASK-005、TASK-006 都 passed：

```bash
relo milestone ready --details
```

Milestone 出現在 ready frontier，但不影響：

```bash
relo task ready
```

Main agent 可選擇暫停新 dispatch，也可繼續執行無關 tasks。

### 7.3 Review 發現需要新 refactor

若 review 確認原 tasks 已符合原 acceptance，但出現新的獨立工作：

```bash
relo task create \
  --title "Refactor storage error handling" \
  --objective "Unify error handling before CLI integration" \
  --accept "All storage errors use the shared error model" \
  --priority 15
# → TASK-007

relo task dependency add TASK-007 TASK-006 \
  --reason "Refactor builds on the completed storage foundation"

relo task dependency add TASK-008 TASK-007 \
  --reason "Downstream integration should use the refactored error model"

relo milestone anchor add MILESTONE-001 TASK-007
```

TASK-007 是 pending，因此 Milestone 自動離開 `ready_to_mark`。既有 TASK-001～TASK-006 IDs 不變。

如果原 task 沒有真正滿足原 acceptance，應使用 `task reopen`，而不是建立一個掩飾原失敗的新 task。

Milestone 不提供排程 barrier，因此 TASK-008 在 review 時可能已是 running 或 passed。上述 dependency mutation 只有在 TASK-008 目前允許修改時才合法：running task 必須先由 harness 停止 worker，再 `task stop`；passed task 必須依現有 downstream safety 由下游往上 reopen；無法安全調整時 CLI 應拒絕，而 Milestone 不得 bypass task mutation rules。

### 7.4 Refactor 完成後 mark

TASK-007 passed 後，Milestone 再次成為 ready：

```bash
relo milestone mark MILESTONE-001 \
  --summary "Reviewed integration boundaries; added and completed TASK-007. Full regression suite passed." \
  --reference "optional-harness-run-or-source-reference"
```

`reference` 不具有 Git 語意；可以是 commit SHA、CI run ID、workspace integration ID 或其他外部識別字串。

## 8. CLI contract

### 8.1 Root commands

```bash
relo milestone create ...
relo milestone get MILESTONE-001
relo milestone list
relo milestone ready
relo milestone update MILESTONE-001 ...
relo milestone delete MILESTONE-001
relo milestone mark MILESTONE-001 ...
```

所有 mutation 只接受 canonical milestone ID。MVP 不提供 title lookup。

Canonical formats：

```text
Milestone ID       ^MILESTONE-[0-9]{3,}$
Recommendation ID  ^REC-[0-9]{3,}$
```

前三位補零，但 sequence 超過 999 時允許更多位數。Canonical validation 必須拒絕負數、空值、非 decimal suffix 與少於三位的 suffix。

### 8.2 Create

```bash
relo milestone create \
  --title "Foundation stable" \
  --reason "Ready for cross-task review" \
  --anchor TASK-003 \
  --anchor TASK-004 \
  --recommend "Review integration boundaries" \
  --recommend "Run full tests"
```

規則：

- `--title` 必填且 trim 後不可為空。
- `--reason` 必填且 trim 後不可為空。
- `--anchor` 可重複，至少一個。
- Anchor IDs 不可重複且 tasks 必須存在。
- `--recommend` 可重複、可省略；每個值 trim 後不可為空。
- 建立與所有 anchors/recommendations 必須在單一 transaction 中完成。
- 成功預設只輸出 ID：

```text
MILESTONE-001
```

### 8.3 Get/list/ready

```bash
relo milestone get MILESTONE-001
relo milestone get MILESTONE-001 --json

relo milestone list
relo milestone list --status planned
relo milestone list --status marked
relo milestone list --json

relo milestone ready
relo milestone ready --details
relo milestone ready --json
```

`list --status` 只接受 stored status：`planned|marked`。Derived `ready_to_mark` 只由 `milestone ready` 提供。

預設排序：

```text
creation order → milestone ID
```

Planned milestone 的 anchors/scope 來自同一 read transaction 內的 live graph；marked milestone 則完全來自 snapshot。兩者都使用 deterministic order。Recommendation 依 position ascending 顯示；第一項 position 為 `0`，新增項目使用 `COALESCE(MAX(position), -1) + 1`，刪除後 position gaps 保留且不重排既有項目。

`milestone get` human output：

```text
# MILESTONE-001 Foundation stable

Status: ready_to_mark
Reason: Ready for cross-task review

## Anchors
- TASK-003 passed  Implement storage
- TASK-004 passed  Implement services

## Scope
- TASK-001 passed  Bootstrap domain
- TASK-002 passed  Add persistence
- TASK-003 passed  Implement storage
- TASK-004 passed  Implement services

## Recommendations
- REC-001 Review integration boundaries
- REC-002 Run full tests

Mark summary: none
Reference: none
```

Marked `get` 使用相同 sections，但 `Status: marked`，anchors/scope 從 snapshot 呈現，並輸出實際 mark summary/reference。

`milestone list` 每行固定為：

```text
MILESTONE-001\tplanned\tFoundation stable
MILESTONE-002\tmarked\tCLI integration stable
```

`milestone ready` 預設每行只輸出 ID：

```text
MILESTONE-001
```

`milestone ready --details` 每行固定為：

```text
MILESTONE-001\tanchors=TASK-003,TASK-004\tFoundation stable
  recommend REC-001: Review integration boundaries
  recommend REC-002: Run full tests
```

沒有結果時 list/ready human output 為空，不輸出 `none`。所有 diagnostic 維持 stderr。

### 8.4 Update

```bash
relo milestone update MILESTONE-001 \
  --title "Backend foundation stable"

relo milestone update MILESTONE-001 \
  --reason "Ready for architecture and integration review"
```

規則：

- 至少一個 update flag。
- 只允許 planned milestone。
- Empty value 失敗。
- Title/reason 可在 ready_to_mark 時修改，因為 ready 仍是 planned derived state。

### 8.5 Anchor CRUD

Atomic batch add：

```bash
relo milestone anchor add \
  MILESTONE-001 \
  TASK-007 TASK-008
```

Atomic batch remove：

```bash
relo milestone anchor remove \
  MILESTONE-001 \
  TASK-005 TASK-006
```

規則：

- 只允許 planned milestone。
- IDs 必須存在且不可重複。
- Add 拒絕既有 anchor。
- Remove 拒絕不存在 anchor。
- Remove 不得讓 milestone 沒有任何 anchor。
- Batch operation 必須 all-or-nothing。
- 加入 pending/running/failed task 合法，但會使 milestone 不再 ready。

### 8.6 Recommendation CRUD

```bash
relo milestone recommendation add MILESTONE-001 \
  --text "Run a security review"

relo milestone recommendation update \
  MILESTONE-001 REC-002 \
  --text "Run a security review if authentication code changed"

relo milestone recommendation remove \
  MILESTONE-001 REC-002
```

規則：

- 只允許 planned milestone。
- Text trim 後不可為空。
- Recommendation ID 在 milestone 內唯一。
- Sequence 單調遞增；刪除後不可重用舊 ID。
- Recommendations 可以全部移除。
- Recommendation position 建立時使用 `COALESCE(MAX(position), -1) + 1`，因此第一項為 0；刪除後允許 gaps，update 不改變 position，human/JSON output 依 position ascending。
- `recommendation add` 成功只輸出新 ID，例如 `REC-003`；update/remove 成功不輸出內容。

### 8.7 Delete

```bash
relo milestone delete MILESTONE-001
```

規則：

- 只允許 planned milestone。
- 同 transaction 刪除 anchors 與 recommendations。
- Marked milestone 不可刪除。
- Delete milestone 不修改或刪除任何 task。

### 8.8 Mark

```bash
relo milestone mark MILESTONE-001 \
  --summary "Integration review and regression tests passed" \
  --reference "optional-reference"
```

規則：

- `--summary` 必填且 trim 後不可為空。
- `--reference` optional，trim 後為空視為未提供。
- Milestone 必須是 planned 且目前 ready_to_mark。
- 完整 scope 中所有 tasks 都必須 passed。
- Mark、snapshot rows、刪除 live anchor rows、timestamps 必須在同一 write transaction 完成。
- Snapshot 以 mark 時 comparator 寫入 contiguous `scope_position`，並以 `is_anchor` 保存 anchor identity。
- 任一 validation 或 write 失敗不得留下 partial snapshot，也不得提前刪除 live anchors。
- Marked 後所有 milestone definition mutation 失敗。

## 9. Task mutation interaction

### 9.1 新增 task

新增 task 永遠使用 project 的 `next_task_sequence`：

```text
目前最大 ID TASK-006
Review 插入新工作
→ 新 ID TASK-007
```

Milestone 不修改 task ID generator。

### 9.2 Reopen

Marked milestone 不阻止 task reopen。Milestone snapshot 是歷史紀錄，不是目前 state 的 constraint。

Planned/ready milestone 的 anchor 被 reopen 後，derived ready 自動消失。

現有 reopen safety 保持不變：若 downstream 有 running 或 passed tasks，必須先依合法順序處理 downstream，不能因 milestone bypass state constraints。

### 9.3 Delete task

若 task 仍被 planned milestone 的 live anchor row 引用，task delete 必須失敗並列出 milestone ID。Agent 必須先移除 anchor 或刪除 planned milestone。

Mark transaction 會把 anchor identity 寫入 snapshot 的 `is_anchor`，再刪除 live anchor rows。因此 marked milestone 不再以 `milestone_anchors.task_id` foreign key 阻止 task delete。Snapshot 保存 task ID/title/order等歷史資料且不 foreign-key 到 tasks，不因 task mutation 改寫。

### 9.4 Priority 與 display order

Review 後插入的新 task ID 可能較大。若 agent 希望它在 ready frontier 優先顯示，應調整 priority 並提供 reason，而不是重新編號。

## 10. Persistence migration

目前 database schema version 為 v1。本功能新增 v2 migration。

Migrator 必須改為 sequential migration runner，不能沿用目前「v0 直接寫入 current version」的單一步驟假設：

```text
v0 fresh database:
  apply schemaV1
  set user_version = 1
  apply migrationV2
  set user_version = 2

v1 existing database:
  apply migrationV2
  set user_version = 2

v2 database:
  no-op

version > 2:
  fail as unsupported
```

每次 `Migrate` 使用單一 immediate transaction 依序套用缺少的 migrations；每個 `user_version` 更新必須在對應 DDL 成功後執行。任一步失敗 rollback 整個 migration transaction，不可留下 DDL 已變更但 version 未更新的半完成 database。

### 10.1 Project sequence

```sql
ALTER TABLE projects
ADD COLUMN next_milestone_sequence INTEGER NOT NULL DEFAULT 1
CHECK (next_milestone_sequence > 0);
```

實作時需先確認 SQLite driver 對 `ALTER TABLE ... ADD COLUMN ... CHECK` 的支援；若有限制，使用等價且可測試的 migration 方式。

### 10.2 Milestones

```text
milestones
  id TEXT PRIMARY KEY
  title TEXT NOT NULL CHECK (title <> '')
  reason TEXT NOT NULL CHECK (reason <> '')
  status TEXT NOT NULL CHECK (status IN ('planned','marked'))
  creation_order INTEGER NOT NULL UNIQUE
  next_recommendation_sequence INTEGER NOT NULL DEFAULT 1
    CHECK (next_recommendation_sequence > 0)
  mark_summary TEXT
  reference TEXT
  created_at TEXT NOT NULL
  updated_at TEXT NOT NULL
  marked_at TEXT
```

Logical consistency：

```text
planned:
  marked_at == NULL
  mark_summary == NULL

marked:
  marked_at != NULL
  mark_summary != NULL and non-empty
```

SQLite CHECK 應盡可能表達此 invariant，service validation 仍需重複確認並回傳可理解錯誤。

### 10.3 Anchors

```text
milestone_anchors
  milestone_id TEXT NOT NULL
    REFERENCES milestones(id) ON DELETE CASCADE
  task_id TEXT NOT NULL
    REFERENCES tasks(id) ON DELETE RESTRICT
  created_at TEXT NOT NULL
  PRIMARY KEY (milestone_id, task_id)
```

`milestone_anchors` 只保存 planned milestone 的 live references。Anchor human/JSON order 由 live task comparator 決定，不保存 user insertion position。Mark transaction 成功後，該 milestone 的 live anchor rows 必須全部刪除。

### 10.4 Recommendations

```text
milestone_recommendations
  milestone_id TEXT NOT NULL
    REFERENCES milestones(id) ON DELETE CASCADE
  recommendation_id TEXT NOT NULL
  text TEXT NOT NULL CHECK (text <> '')
  position INTEGER NOT NULL CHECK (position >= 0)
  created_at TEXT NOT NULL
  updated_at TEXT NOT NULL
  PRIMARY KEY (milestone_id, recommendation_id)
  UNIQUE (milestone_id, position)
```

### 10.5 Snapshots

```text
milestone_snapshots
  milestone_id TEXT NOT NULL
    REFERENCES milestones(id) ON DELETE RESTRICT
  task_id TEXT NOT NULL
  task_title TEXT NOT NULL
  priority INTEGER NOT NULL CHECK (priority >= 0)
  creation_order INTEGER NOT NULL CHECK (creation_order > 0)
  scope_position INTEGER NOT NULL CHECK (scope_position >= 0)
  is_anchor INTEGER NOT NULL CHECK (is_anchor IN (0,1))
  attempt_number INTEGER NOT NULL CHECK (attempt_number > 0)
  status TEXT NOT NULL CHECK (status = 'passed')
  completion_summary TEXT
  task_updated_at TEXT NOT NULL
  captured_at TEXT NOT NULL
  PRIMARY KEY (milestone_id, task_id)
  UNIQUE (milestone_id, scope_position)
```

Snapshot 的 `task_id` 故意不 foreign-key 到 tasks，讓歷史 checkpoint 不依賴 mutable task row。Marked milestone 的 anchors 是 `is_anchor=1` rows；scope 依 `scope_position` 輸出，不再讀 live task priority、creation order 或 dependencies。

### 10.6 Transaction rules

沿用現有 store 規則：

- `BEGIN IMMEDIATE` 或 driver equivalent。
- Busy retry 重跑完整 transaction closure。
- Lock 後重新讀取 milestone、anchors、graph 與 task state。
- Create、batch anchor mutation、recommendation mutation、delete、mark 都必須 atomic。
- `mark` 與 concurrent task reopen/anchor mutation 必須序列化，不可使用 lock 前 snapshot。
- `milestone get/list/ready`、含 Milestone 的 `status` 與 `validate` 必須各自在單一 read transaction 中組裝 milestone、anchors、recommendations、task states 與 graph，避免 mixed snapshots。
- Marked read path 只能讀 milestone row、recommendations 與 snapshots，不得依賴目前 task/dependency rows。

## 11. JSON contract

沿用：

```json
{
  "schemaVersion": "relo.output/v1",
  "ok": true,
  "data": {}
}
```

Milestone JSON 使用以下明確 DTO，不輸出 raw domain structs。

Task reference DTO（`anchors` 與 `scope` 共用）：

```json
{
  "task_id": "TASK-003",
  "title": "Implement storage",
  "status": "passed",
  "priority": 20,
  "creation_order": 3,
  "attempt_number": 1,
  "is_anchor": true
}
```

Recommendation DTO：

```json
{
  "id": "REC-001",
  "text": "Review integration boundaries",
  "position": 0
}
```

Full milestone DTO：

```json
{
  "id": "MILESTONE-001",
  "title": "Foundation stable",
  "reason": "Ready for cross-task review",
  "stored_status": "planned",
  "display_status": "ready_to_mark",
  "anchors": [
    {
      "task_id": "TASK-003",
      "title": "Implement storage",
      "status": "passed",
      "priority": 20,
      "creation_order": 3,
      "attempt_number": 1,
      "is_anchor": true
    }
  ],
  "scope": [
    {
      "task_id": "TASK-001",
      "title": "Bootstrap domain",
      "status": "passed",
      "priority": 10,
      "creation_order": 1,
      "attempt_number": 1,
      "is_anchor": false
    },
    {
      "task_id": "TASK-003",
      "title": "Implement storage",
      "status": "passed",
      "priority": 20,
      "creation_order": 3,
      "attempt_number": 1,
      "is_anchor": true
    }
  ],
  "recommendations": [
    {
      "id": "REC-001",
      "text": "Review integration boundaries",
      "position": 0
    }
  ],
  "mark_summary": null,
  "reference": null,
  "created_at": "2026-07-11T10:00:00Z",
  "updated_at": "2026-07-11T10:00:00Z",
  "marked_at": null
}
```

`anchors` 中每個 task DTO 的 `is_anchor` 必為 true。`scope` 包含 anchors 與 dependency closure，每個 row 以 `is_anchor` 表示 mark/planned scope 中是否為明列 anchor。Planned DTO 從同一 read snapshot 的 live task rows產生；marked DTO 的 task fields 全部來自 snapshots。Marked DTO 的 `mark_summary`、`marked_at` 必為非空 string；`reference` 仍可為 null。

Summary milestone DTO（`list` 與 `ready` 共用）：

```json
{
  "id": "MILESTONE-001",
  "title": "Foundation stable",
  "stored_status": "planned",
  "display_status": "ready_to_mark",
  "anchor_task_ids": ["TASK-003", "TASK-004"],
  "recommendations": [
    {
      "id": "REC-001",
      "text": "Review integration boundaries",
      "position": 0
    }
  ],
  "created_at": "2026-07-11T10:00:00Z",
  "marked_at": null
}
```

Summary `anchor_task_ids` 使用與 full anchors 相同順序；recommendations 使用完整 Recommendation DTO 並依 position ascending。Marked summary 的 anchor IDs 從 snapshot `is_anchor=1` rows取得。

所有 arrays 即使為空也輸出 `[]`，不可為 `null`。Optional scalar 使用 JSON `null`，不可省略欄位。JSON errors 維持 stdout envelope、non-zero exit code 與 stderr empty 的既有規則。

本次只有 read commands 提供 JSON flags；create/update/delete/anchor/recommendation/mark mutations 維持 human output，不在 MVP 新增 `--json`。

Envelope data mapping 固定為：

```text
milestone get MILESTONE-001 --json
  data.milestone = exactly one Full milestone DTO

milestone list --json
  data.milestones = Summary milestone DTO array

milestone ready --json
  data.milestones = ready_to_mark Summary milestone DTO array
```

`data` 不得包含上述 mapping 以外的 sibling milestone fields。`list` 依 milestone creation order；`ready` 只含 ready_to_mark milestones 並使用相同排序。零筆結果必須是 `{"milestones":[]}`。Contract tests 必須驗證 exact field names、nullability、array shape 與 ordering。

## 12. Status、graph 與 validate

### 12.1 Status

Human output 增加：

```text
Running: TASK-008
Ready:   TASK-009
Blocked: TASK-010
Failed:  none

Milestones ready to mark: MILESTONE-001
```

若沒有：

```text
Milestones ready to mark: none
```

JSON status 在既有 `data.summary` 內增加 non-null array：

```json
{
  "schemaVersion": "relo.output/v1",
  "ok": true,
  "data": {
    "summary": {
      "running": ["TASK-008"],
      "ready": ["TASK-009"],
      "blocked": ["TASK-010"],
      "failed": [],
      "milestones_ready_to_mark": ["MILESTONE-001"]
    }
  }
}
```

此欄位採 additive-compatible 加入 `relo.output/v1`，並以 contract test 固定位置與 non-null array。若未來發現既有 consumers 拒絕 unknown fields，再另行升版；本次不建立第二種 status shape。

### 12.2 Graph

Human-readable tree output 在 task dependency tree 後加入獨立的 non-gating checkpoint overlay，預設呈現 planned 與 ready milestones；`--all-milestones` 另含 marked history，`--tasks-only` 可只呈現 task tree。Milestone 不會成為 task node、dependency edge 或 dispatch barrier，`relo graph --format json` 仍維持純 task DAG contract。

### 12.3 Validate

Errors：

- Milestone title/reason empty。
- Planned milestone 沒有 live anchor。
- Planned anchor reference 不存在或 duplicate。
- Marked milestone 仍殘留 live anchor rows。
- Marked milestone 沒有 marked timestamp 或 mark summary。
- Planned milestone 意外擁有 snapshot rows。
- Marked milestone 沒有 snapshot，或 snapshot 中沒有任何 `is_anchor=1` row。
- Snapshot `scope_position` 重複、不從 0 contiguous 遞增，或 stored priority/creation order 無效。
- Snapshot status 不是 passed 或 attempt number 非正數。
- Recommendation text empty、duplicate ID 或 invalid position。
- Recommendation positions duplicate；gaps 合法。
- Sequence 非正數或不大於已產生的最大 canonical ID suffix。

`validate` 不嘗試以目前 live dependencies 重建 marked milestone 的歷史 closure；dependency drift analysis 是非目標。Mark-time transaction 已驗證 closure，之後只驗證 immutable snapshot 自身結構。

Recommendations 是 optional，因此沒有 recommendation 不產生 warning。

## 13. Agent skill guidance

文件/skill 應提供範例，而不是把 recommendation 行為硬編碼進 CLI：

```text
When `relo milestone ready` returns a checkpoint:

1. Read its scope and recommendations.
2. Consider the combined diff, architecture risk, test coverage,
   integration risk, remaining tasks, context, and execution budget.
3. Decide autonomously whether to:
   - run a reviewer;
   - run a read-only refactor sweeper;
   - run broader tests;
   - reorder future work;
   - add a different check;
   - skip checks that add little value;
   - mark immediately.
4. If an original task did not satisfy its own acceptance criteria,
   reopen that task.
5. If the finding is distinct new work, create a new task. Existing
   task IDs never change.
6. Update dependencies and milestone anchors when new work is required
   before the checkpoint can be marked.
7. Non-trivial code changes proposed by a sweeper should be regular
   tasks with acceptance criteria and attempts.
8. Mark only when the checkpoint is sufficiently stable, and summarize
   which recommendations were followed or skipped and why.
```

Worker 預設只負責自己的 task acceptance，不應自行 mark Milestone；Milestone 決策由具有完整 DAG/integration context 的 main agent 負責。

## 14. Go architecture changes

建議新增：

```text
internal/
├── domain/
│   └── milestone.go
├── store/
│   ├── milestone.go
│   └── migrations.go          # v2 migration
├── milestone/
│   ├── service.go
│   ├── scope.go
│   └── validation.go
├── render/
│   └── milestone.go
└── cli/
    └── milestone.go
```

若目前 repository 實際檔案組織與原 plan 不同，應沿用現有模式，不為了符合示意架構進行無關重構。

Domain、scope calculation 與 store service 不依賴 Cobra。Scope traversal 可重用 `internal/dag` comparator 與 graph data，但不得把 Milestone node 加入 task DAG。

## 15. 實作工作 DAG

| ID | 工作 | Depends on |
|---|---|---|
| MS-TASK-001 | Add v2 migration and Milestone domain contract | — |
| MS-TASK-002 | Implement Milestone CRUD, anchors, scope closure, and derived ready state | MS-TASK-001 |
| MS-TASK-003 | Implement free-form recommendations and atomic immutable mark snapshot | MS-TASK-002 |
| MS-TASK-004 | Add CLI, status/JSON integration, and full validation | MS-TASK-003 |
| MS-TASK-005 | Add agent guidance and end-to-end milestone review scenario | MS-TASK-004 |

```text
MS-TASK-001  Schema/domain
    └── MS-TASK-002  CRUD/anchors/scope/ready
        └── MS-TASK-003  Recommendations/mark snapshot
            └── MS-TASK-004  CLI/status/JSON/validate
                └── MS-TASK-005  Skill guidance/E2E
```

## 16. Testing strategy

### 16.1 Domain/unit tests

- Milestone、recommendation ID generation。
- Canonical ID validation。
- Planned/marked mutation restrictions。
- Ready state with pending/running/passed/failed anchors。
- Empty anchors rejected。
- Recommendation text validation。
- Recommendation sequence does not reuse removed IDs。

### 16.2 Scope tests

- Single anchor with no dependencies。
- Chain dependency closure。
- Diamond DAG deduplication。
- Multiple anchors with overlapping closure。
- Redundant anchors（其中一個依賴另一個）合法且 scope 去重。
- Multiple roots。
- Deterministic comparator ordering。
- Missing task reference。
- Corrupted cycle handling。
- Marked scope rendering 不讀 live graph。

### 16.3 Migration/store tests

- Existing v1 database migrates to v2 without losing project/tasks/attempts/events。
- Fresh v0 database sequentially applies v1 then v2，最後 user_version=2。
- v2 database migration no-op；version >2 明確失敗。
- 任一 v2 DDL failure rollback schema 與 user_version。
- `next_milestone_sequence` persists across restart。
- Create with batch anchors/recommendations is atomic。
- Anchor add/remove batch rollback。
- Planned milestone delete cascades only milestone-owned rows。
- Planned anchored task delete is rejected，錯誤列出所有引用該 task 的 planned milestone IDs。
- Mark captures complete closure、anchor flags、排序欄位與 current attempt numbers。
- Mark atomically刪除 live anchor rows；marked snapshot 不再阻止 task 在符合既有規則後刪除。
- Mark rollback leaves no partial snapshot，也保留原 live anchors。
- Marked milestone is immutable。
- Marked `get` 在 task reopen、priority update、definition update 或合法 delete 後仍 byte-for-byte stable（timestamps 以固定 fixture 比較）。
- Concurrent mark and anchor mutation serialize correctly。
- Concurrent mark and task reopen outcomes are consistent：
  - reopen first → mark sees non-passed anchor and fails；
  - mark first → immutable snapshot remains valid even if task later reopens。
- Read commands 在 concurrent mutation 下不混用 milestone/task/graph snapshots。

### 16.4 CLI integration tests

- Create → get → list → update → delete planned milestone。
- Reject malformed canonical milestone/recommendation IDs。
- Reject create without title/reason/anchor。
- Reject missing/duplicate anchors without partial mutation。
- Add/update/remove recommendation，確認 ID 不重用、position gaps 合法且排序固定。
- Ready output only includes milestones whose anchors passed。
- Milestone ready does not change `task ready` or block task start。
- Human output涵蓋 `list`、`ready --details`、marked/planned `get`；`recommendation add` 回傳 generated ID。
- Human and JSON output use deterministic ordering/non-null arrays。
- Planned/marked `get`、`list`、`ready --json` 使用 exact golden contract tests 固定完整 DTO、envelope/data shape、nullability 與 ordering。
- Mark rejects pending/running/failed anchor。
- Mark success captures summary/reference and prevents further mutation。
- Marked `get` 從 snapshot 輸出 anchors/scope。
- `status --json` 將 `milestones_ready_to_mark` 放在 `data.summary`。
- Validation reports corrupted milestone state，但不以 live DAG 驗證 marked historical closure。
- Exit codes and JSON stdout/stderr follow existing contract。

### 16.5 End-to-end scenario

1. 建立 6 個 tasks 與 dependency DAG。
2. 建立 milestone，以 TASK-003、TASK-004 為 anchors，附 review/refactor/test recommendations。
3. 完成 anchors，確認 milestone ready，且 downstream task 仍可由 main agent自由選擇。
4. 啟動 reviewer，發現 distinct refactor work。
5. CLI 建立新 task，確認取得下一個新 ID，既有 IDs 不變。
6. 將新 task 插入 downstream DAG 並加入 milestone anchor。
7. 確認 milestone 不再 ready。
8. 完成 refactor task，確認 milestone 再次 ready。
9. Mark milestone 並驗證 immutable snapshot。
10. Reopen snapshot 中的 task，確認 marked history 不變。
11. Process restart 後 milestone、recommendations、snapshot 仍存在。
12. 全流程不直接讀寫 `.relo/relo.db`。

## 17. Definition of Done

- Milestone 是獨立 checkpoint model，不是 Task。
- Milestone 不產生 attempt、不進入 pending/running/passed/failed task state。
- Milestone 不阻擋 task ready frontier 或 start。
- Agent 可使用一或多個 anchors 定義 checkpoint scope。
- Scope 正確包含 anchors 的 transitive dependency closure，且 deterministic。
- 所有 anchors passed 時，planned milestone 動態出現在 `milestone ready`。
- Recommendations 是 optional free-form strings，沒有 enum 或 completion state。
- Agent 可在 ready checkpoint review 後建立新 task、修改 DAG、加入新 anchor。
- 新 task 使用下一個 ID；既有 task IDs 永遠不改變、不重用。
- 新 pending anchor 使 milestone 自動離開 ready，完成後再自動回到 ready。
- Mark transaction 重新驗證完整 scope，atomic 寫入含排序/anchor metadata 的 immutable snapshot，並刪除 live anchors。
- Marked milestone 不可修改、刪除或重寫。
- Marked `get` 只從 snapshot 呈現 scope/anchors，不依賴 live task graph。
- Marked snapshot 不因 task reopen、priority/definition update 或後續合法刪除而改變。
- Optional reference 保持 opaque；實作不依賴 Git 或 harness。
- Human/JSON output 與 exit codes 符合既有 CLI contract。
- v1 database 可安全 migration，既有資料不遺失。
- 完整 E2E scenario 通過，且 agent 不需要知道 SQLite schema。

## 18. 後續可能方向

以下只記錄，不納入本次：

- Milestone supersede/deprecate，而非 rewrite marked history。
- Snapshot/current-state drift report。
- Milestone timeline/history UI。
- Recommendation templates 或 reusable skill recipes。
- Harness 將 reviewer run ID、CI ID 或 workspace integration ID 寫入 reference。
- Read-only reviewer result attachment。
- Worker report 與 main-agent approval protocol。
- 通用 `--root` / `RELO_PROJECT_ROOT`，讓 isolated workspace 共用 canonical task store。
- Harness-specific workspace skills。
- 獨立 worktree/container workspace manager CLI。
