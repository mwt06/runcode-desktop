# Automation implementation log

## Step 0 — baseline

| Field | Value |
| --- | --- |
| Date | 2026-11-18 |
| Implementation worktree | `C:\Users\yyzxc\.codex\worktrees\automation-tasks-1118\imagegen-client` |
| Branch | `codex/automation-tasks-1118` |
| Start HEAD | `59cb3eeb7d5da1262a1ba7dd9f908143d42436aa` (`feat/upgrade-1118` tip) |
| Spec | `docs/automation/01-自动化任务执行方案.md` |
| Handbook | `docs/automation/02-自动化任务分步执行手册.md` |

### Main checkout (left untouched)

Path: `C:\Users\yyzxc\Desktop\Client-Platform\imagegen-client` on `feat/upgrade-1118`.

Dirty at Step 0 start (not carried into this worktree):

- `cmd/runcode-desktop/build/config.yml`, `build/darwin/Info.plist`, `build/windows/info.json`
- `frontend` inline-preview / static-html / static-detect edits
- `.build-logs/`

### Toolchain (this session)

```
GOWORK=off
GOPRIVATE=gitlab.ouc-online.com.cn
go 1.26.0 windows/amd64
gcc 8.1.0 (mingw)
node v24.20.0
npm 11.19.0
```

### Baseline commands

| Command | Result |
| --- | --- |
| `go test ./internal/desktop ./internal/protocol` | PASS (`desktop` ~44s) |
| `go run ./tools/protogen --check` | PASS |
| `npm run typecheck` (frontend) | PASS |
| `npm run lint` (frontend) | PASS |
| `npm test` (vitest) | **1 fail / 902 pass** — `inline-file-preview.test.tsx` expects img click → open side preview (known stale vs `59cb3ee`; not part of automation). Logs in `_baseline/` |

Step 0 complete.

## Step 1 — schedule

| Field | Value |
| --- | --- |
| Commit | `feat(automation): add validated schedule calculation` |
| Tests | `go test ./internal/automation -run 'TestNextOccurrence|TestScheduleDST|TestValidateSchedule|TestCountOccurrencesBounded' -count=1` → PASS |
| Files | `internal/automation/{types,schedule,schedule_test}.go` |

## Step 2 — store

| Field | Value |
| --- | --- |
| Commit | `feat(automation): persist tasks runs and session links` |
| Tests | `go test ./internal/automation -run TestStore -count=1` → PASS |
| Race | `go test -race` failed with Windows `0xc0000139` (toolchain DLL); non-race suite green |
| Files | `internal/automation/{errors,store_types,migrations,store,store_test}.go` |

## Step 3 — scheduler service

| Field | Value |
| --- | --- |
| Commit | `feat(automation): schedule bounded background runs` |
| Tests | `go test ./internal/automation -count=3` → PASS |
| Files | `internal/automation/{service,runner,service_test,fake_clock_test}.go`; Recover leaves queued for re-check |
| Notes | Global active=1; per-task queued≤1; miss>60s aggregated; queue wait>60s → busy; 60m timeout holds slot until Fact; Observe non-blocking |

## Step 4 — desktop runner and identity

| Field | Value |
| --- | --- |
| Commit | `feat(automation): run isolated sessions with bound identities` |
| Tests | `go test ./internal/desktop ./internal/automation ./internal/codexauth ./internal/codexproxy -count=1` → PASS (`desktop` 49.2s, `automation` 1.5s, `codexauth` 1.5s, `codexproxy` 1.1s) |
| Race | Not run. Step 2 `-race` on this Windows toolchain exits `0xc0000139`. This step does not claim a race PASS. |
| Files | `internal/desktop/automation_{runner,identity,lease,runner_test,identity_test}.go`; guards in `app.go`, `questions.go`, `turn.go`, `sessions.go`, `session_settings.go`, `mcp.go`; identity in `tokens.go`, `codex.go`, `custommodels.go`, `internal/protocol/custommodels.go`, `internal/codexauth`; receipt merge in `internal/automation/{runner,service}.go` |

Notes:

- Runs open with `buildSessionModeHeld(..., focus=false)` under `startMu`. They do not call `OpenSession`. Workspace comes from the task. `AllowSteering` is forced off. The lease is installed before submit.
- `turn:end` with `Stopped=true` is interrupted, not success. A success that arrives first is kept. Timeout and other errors map to `timed_out` and `failed`. Emit only queues a fact; the unfocused session closes on a later worker.
- Passport identity is issuer+subject plus the selected tenant. Codex OAuth persists `iss`/`sub`; a login that has an account id but no subject must sign in again. Static keys use `credentialRevision`, bumped only when the key actually changes. Old configs with a key and no revision count as 1.
- The Codex proxy package is unchanged. While a run is leased, the desktop resolver checks the bound subject or key revision before it returns a bearer.
- Not accepted here: a real Passport, Codex, or OA round trip, or the Windows race detector.

Next: Step 5 command facade and protocol.

## Step 5 — desktop commands and events

| Field | Value |
| --- | --- |
| Commit | `feat(automation): expose scoped task commands and events` |
| Tests | `go test ./internal/desktop ./internal/automation ./internal/protocol -count=1` → PASS (`desktop` 45.952s, `automation` 1.398s, `protocol` no test files) |
| Protocol | `go run ./tools/protogen` then `go run ./tools/protogen --check` → PASS |
| Frontend | `npm run typecheck` in `cmd/runcode-desktop/frontend` → PASS |
| Race | Not run. Step 2 `-race` on this Windows toolchain exits `0xc0000139`. This step does not claim a race PASS. |
| Files | `internal/desktop/{automation,automation_test,app,exit,passport,codex,protocol_aliases,automation_lease,automation_runner,automation_runner_test}.go`; `internal/protocol/{automation,commands,events,session,conversation}.go`; `internal/automation/{store,service}.go`; `tools/protogen/extract.go`; generated `frontend/src/core/protocol/{types,commands,events}.ts`; `frontend/src/core/bridge.ts` |

Notes:

- `New` does not open the task database. `Startup` opens `UserConfigDir/runcode/automation/<AppProduct()>/automations.sqlite3`, then Recover and Service.Start. A bad product segment or open/start failure leaves chat usable and `defaults.available=false` with a reason that does not include the path. `PrepareExit` stops the service before closing the store. `HoldAdmission`, `PrepareShutdown`, and `ExitSummary` stay reachable for Step 10.
- The authorized owner set is host-computed. A non-empty client `ownerScope` is rejected. Local static/Codex API-key tasks stay on `local:<product>:<user hash>` when the selected model changes. Passport and Codex OAuth owners join the set only with a subject (and, for Passport, a tenant and bridge). The same subject with a new `jti` does not bump the epoch. Login, logout, and tenant change bump the epoch and emit `automation:context_changed` before later content.
- `defaults.bindingVersion` is an in-memory stamp of the current binding digest and static credential revision. The value stored on the task stays schema `BindingVersion` 1, because run validation requires that constant. Create checks an existing `clientRequestId` across still-authorized owners before it checks the stamp, so a later key change returns the original task instead of creating a second one.
- Data commands re-check the context after an optional pre-return hook. Task and run events are emitted only after persist, and only while that owner is still authorized. Host envelopes keep Core `seq` and payload. `automationSource.context` is the context at emit time. An unauthorized lease still finishes internally and is not forwarded. Process automation events use an empty `sessionId`.
- `GetAutomationConversation` does not change focus. A busy or not-ready live session returns `historyPending=true` and `blocks=[]`. An idle in-manager session is read under `WithIdleSession` and passed through `IdentifyQuestions`. A missing transcript is `not_found`. `OpenAutomationSession` on a live session only focuses it. A closed session is resumed from the session link's workspace.
- Successful create records the workspace with `mergeRecentWorkspaces` and `updateRawConfig` only. It does not change CWD, Resume, or the connection. If that write fails, the task remains and the result warning says the directory history was not updated.
- SQLite uses one connection. Listing ids and then loading each row while that result set was still open deadlocked `TaskByClientRequestForOwners`, `claimDueIntoQueue`, and `reloadQueued`. Those paths now close the rows before the follow-up query.
- `bridge.onEnvelope` returns `DesktopEnvelope<P>`. Generated `events.ts` still delivers the Core `Envelope`. Pages call the new commands through `bridge`, not `Call.ByName`.

Deferred:

- Step 6 fills `OpenSessionInfo` / `SessionSummary` / `ConversationInfo.automation` from `session_link` and projects execution cards. Step 5 only adds the optional field. Kind stays `agent`.
- Step 10 supplies the Windows `WindowController`, tray, and confirm-exit. A nil controller returns `automation_unavailable`. `BackgroundSupported` stays false.
- No real Passport, Codex, or OA round trip. No race-detector PASS.

Next: Step 6 execution cards, history projection, and cross-workspace open.

## Step 6 — execution cards and linked chats

| Field | Value |
| --- | --- |
| Commit | `feat(automation): replay execution cards and mark linked chats` |
| Tests | `go test ./internal/desktop -run Automation -count=1` → PASS (`desktop` 1.715s). `go test ./internal/automation -count=1` → PASS (1.543s). |
| Frontend | `npm.cmd test -- src/chat/automation-run.test.ts src/session/automation-turns.test.ts` → PASS, together with `execution-card.test.tsx`, `tool-text.test.ts`, `use-conversation.test.tsx`, and `use-session.test.tsx` (6 files, 51 tests). `npm.cmd run typecheck` → PASS. `npm.cmd run lint` → PASS. |
| Race | Not run. Step 2 `-race` on this Windows toolchain exits `0xc0000139`. This step does not claim a race PASS. |
| Files | `internal/automation/store.go`; `internal/desktop/{app,automation,automation_lease,automation_replay_test,automation_test,multisession,protocol_aliases,sessions}.go`; `frontend/src/chat/{automation-run,automation-run.test,execution-card,execution-card.test,tool-detail,tool-text,tool-text.test}.{ts,tsx}`; `frontend/src/session/{automation-turns,automation-turns.test,use-conversation,use-conversation.test,use-session}.{ts,tsx}`; `frontend/src/{App.tsx,dev/previews.tsx,shell/sidebar.tsx,ui/icons.tsx}` |

Notes:

- Execution cards are a desktop projection. `toolUseId` is `automation:` plus the run id. Core `seq` is not rewritten and no fake tool is written into history. A run is placed after the user question that owns it. No `questionId` means no guessed user and no card. Another session's question is not a place to hang the card.
- The first leased delta inserts the SystemNote “正在提交自动化任务” so the new answer does not merge into the previous assistant. The receipt then replaces that note with the real user and one summary card. A terminal snapshot does not regress to running. A late receipt only fills `questionId`. After the run is settled, busy stays false.
- The summary row stays collapsed. The spinner shows until the run succeeds. The green check is only `succeeded`. `failed`, `interrupted`, `timed_out`, and `skipped` stay red. Prompt text is rendered as text.
- `OpenSessionInfo`, `SessionSummary`, and `ConversationInfo.automation` come from `session_link`, joined to the current task name. `Kind` stays `agent`. The lookup runs after `App.mu` is released. `OpenSessions.Running` is the live turn or the lease. The sidebar also ORs the focused busy flag and the `run_changed` flag, so a background run shows a clock and a running dot before that session is opened.
- `run_changed` refreshes the open list and the recent list even when the run's session is not focused. History paging walks every cursor. `historyPending` keeps the blocks already on screen. A terminal refresh merges by `questionId` / `toolUseID` and does not call `applyResumed` over a live stream. Async loads are dropped when either the session generation or the automation context moves.
- `TestAutomationReplayLinkAndWorkspace` uses two workspaces with the same title. Opening the run resumes the linked directory. A missing transcript returns “会话历史不存在” and does not build an empty conversation. After restart and a rename, the clock still resolves to the same task id and the new name.
- Manual browser check completed on 2026-11-19 through `?preview=tools`: the automation card is collapsed by default; expanding it shows task, scheduled time, workspace, execution mode, prompt and output. Evidence: `docs/automation/validation/step6-execution-card.jpg`.

Deferred:

- Step 7 owns the more menu and the task manager. Step 8 owns the form. Step 9 owns detail and pause. Step 10 owns the tray, `WindowController`, and real exit. `BackgroundSupported` stays false.
- No real Passport, Codex, or OA round trip. No race-detector PASS.
- `?preview=tools` now includes an automation summary fixture.

Next: Step 7 task list and the more-menu entry.

## Step 7 — more menu and task manager

| Field | Value |
| --- | --- |
| Commit | `feat(automation): add more menu and task manager` |
| Frontend | `npm.cmd test -- src/session/automation-state.test.ts src/shell/more-menu.test.tsx src/pages/automation/workspace-dialog.test.tsx` → PASS (3 files, 7 tests). `npm.cmd run typecheck` → PASS. `npm.cmd run lint` → PASS. |

Notes:

- “更多” sits under 市场. The icon is a four-plus grid in the existing stroke style. The menu registry currently has one entry, 自动化任务. Opening it does not change the chat view and does not call `openSession`, `pickWorkspaceFolder`, or `resumeSession`.
- The manager is a modal on the app root, not a view and not a preview tab. It can open with no live session. Esc closes the create drawer first, then the window. Tab stays inside the dialog. Closing returns focus to the control that opened it and does not touch the composer draft.
- List state is a reducer. An event that arrives before the snapshot is kept when its revision is higher. The same context ignores an older revision. A new context drops the cache and late responses. A slow page for the previous search does not replace the current query. Pages append without duplicating ids.
- Tabs are 已开启 / 已暂停 / 已结束 / 全部. Search matches the current result set’s name and prompt only through the backend query. Empty search and a filter miss use different copy. The create control is centered when the list is empty and sits on the search row when it is not. The create drawer is a placeholder until Step 8 and does not save or run anything. The list formats `nextRunAt` for display and does not schedule work itself.
- The production manager components were rendered at 1024×680 and 150% scale. Title, close control, tabs, search, task rows and the create action remain readable; content scrolls inside the window. Evidence: `docs/automation/validation/step7-manager-1024x680.jpg` and `step7-manager-150pct.jpg`.

Deferred:

- Step 8 replaces the create placeholder with the seven schedule modes and the native workspace picker. Step 9 owns detail, pause/resume, and run history. Step 10 owns the tray, `WindowController`, and real exit. `BackgroundSupported` stays false.
- No real Passport, Codex, or OA round trip. No race-detector PASS.

Next: Step 8 create form and native workspace picker.

## Step 8 — create form and native workspace picker

| Field | Value |
| --- | --- |
| Commit | `feat(automation): create schedules with native workspace picker` |
| Frontend | `npm.cmd test -- src/pages/automation/draft.test.ts src/pages/automation/workspace-picker.test.tsx src/pages/automation/task-form.test.tsx src/pages/automation/workspace-dialog.test.tsx src/shell/more-menu.test.tsx` → PASS (5 files, 11 tests). `npm.cmd run typecheck` → PASS. `npm.cmd run lint` → PASS. |

Notes:

- The create drawer replaces the Step 7 placeholder. The schedule control offers exactly weekly, weekdays, daily, every_hours, every_minutes, monthly, and once. There is no custom mode and no cron field. Repeating modes ask for an interval of 1–9999 with the unit 周/天/小时/分钟/月. Once omits the interval. Weekly can select several weekdays. Monthly warns that a missing day is skipped.
- The default is daily, at the next five-minute boundary with seconds 00. A boundary after 23:55 rolls the start date forward. Name and prompt lengths count Unicode code points, so a non-BMP character or an emoji is one character. 80 and 16000 are accepted; 81 and 16001 are not.
- The workspace picker searches the backend recent list and ends with “浏览其他目录...”. `pickWorkspaceFolder` is the only directory call. An empty cancel keeps the previous path. A new path only updates the draft. It does not call `openSession` or `resumeSession`, and it does not write localStorage.
- Save sends `createAutomationTask` with the current context, `expectedBindingVersion`, and one `clientRequestId`. A failed attempt keeps the text and the same id. Success or an explicit cancel is what retires the id. The button ignores a second click while the request is in flight. Success does not call send or run. The model name and permission mode are read-only. A missing savable connection disables save and offers settings. An unrecognized browser zone is shown as Asia/Shanghai. A backend “expired once” error keeps the draft.
- Closing a dirty drawer asks with ConfirmDialog before discarding. Enter is not treated as save while an IME composition is in progress.
- The seven schedule options and the full create drawer were rendered at 1024×680 and 150%; the bottom 取消／保存 actions are reachable in the internal scroll region. Evidence: `docs/automation/validation/step8-form-1024x680.jpg`, `step8-form-150pct.jpg`, and `step8-form-150pct-bottom.jpg`.
- `TestAutomationWorkspacePickerContract` verifies a selected path, an empty cancellation, and the unsupported-dialog error. The existing native Wails directory picker remains the production implementation.

Deferred:

- Step 9 owns detail, pause/resume, and run history. Step 10 owns the tray and real exit. `BackgroundSupported` stays false.
- No real Passport, Codex, or OA round trip. No race-detector PASS.

Next: Step 9 task detail, pause/resume, and run history.

## Step 9 — task details, pause/resume, and run history

| Field | Value |
| --- | --- |
| Commit | `feat(automation): add task details pause resume and run history` |
| Frontend | `npm.cmd test -- src/pages/automation/task-detail.test.tsx src/pages/automation/run-list.test.tsx src/pages/automation/task-form.test.tsx src/pages/automation/workspace-dialog.test.tsx src/shell/more-menu.test.tsx` → PASS (5 files, 10 tests). Full Vitest → 938 pass / 1 known baseline failure. |
| Checks | `npm.cmd run typecheck`, `npm.cmd run lint`, `npm.cmd run build` → PASS. |
| Go | `go test ./internal/desktop -run Automation -count=1`, `go test ./internal/automation -count=1`, and `go run ./tools/protogen --check` → PASS. |
| Files | Adds `pages/automation/{task-detail,run-list}.tsx` and their tests; extends the manager/dialog controller, task filtering and Step 8 field enablement. |

Notes:

- The detail drawer has rule/run-record tabs, revision-conflict refresh without discarding the draft, active-run rule locking, paused-with-active-run messaging, and pending pause/resume labels.
- Run history is newest first, paginated by cursor, de-duplicates ids, preserves row order when a run event updates an older row, copies failure reasons, and shows a green check only for `succeeded`.
- Clicking linked history calls the existing `useSession.openAutomationSession`, which performs the real cross-workspace switch, history merge/focus behavior and context checks. The manager closes only after that callback succeeds; failures remain visible in the run list.
- Enabled/paused/ended filter movement is driven by authoritative task events. Existing service tests continue to cover pause/resume scheduling and expired one-shot behavior.

Deferred:

- Step 10 owns tray background residency, branded tray menu, close-to-stay and confirmed exit. `BackgroundSupported` stays false.
- No real Passport, Codex, or OA round trip. No race-detector PASS.

Next: Step 10 Windows background residency and confirmed exit.

## Step 10 — Windows tray, background residency, and confirmed exit

| Field | Value |
| --- | --- |
| Commit | `feat(desktop): keep scheduled tasks running in branded tray` |
| Lifecycle tests | `go test ./internal/desktop -run 'Window\|Exit\|PrepareExit\|AutomationWindowController'` → PASS |
| Automation | `go test ./internal/automation -count=10` → PASS, including shutdown admission, blocked drain and paused-active exit counts |
| Desktop module | `go -C cmd/runcode-desktop test ./...` and `go -C cmd/runcode-desktop build ./...` → PASS |
| Frontend | title-bar lifecycle tests plus all automation tests/typecheck/lint → PASS |

Notes:

- `WindowLifecycle` owns close/exit policy. A supported host close only hides and restores focus without cleanup. Real exit queries enabled and unfinished counts, uses the native question before taking any irreversible action, and treats an unreadable summary conservatively.
- Confirmation is deduplicated. Cancel performs no hold or cleanup. Confirm acquires the scheduler hold before `PrepareExit`, then runs the shared 20-second cleanup and quits. Hold/cleanup setup failures keep the process alive and report an error.
- Windows embeds the branded `build/appicon.png` in its system tray. Left/double-click restores the existing window; right-click refreshes the pending-run menu and opens it. No `AttachWindow` is used.
- The tray pending entry restores the window and emits `automation:open_manager`; the frontend bridge routes it to the existing management overlay.
- The title-bar close now calls `closeMainWindow()`. Native close and title-bar close share the same hook; Kylin/V2 uses exit behavior instead of claiming tray support.
- `BackgroundSupported` now comes from the installed host. On platforms without a tray, the automation window explicitly warns that closing the client stops tasks.
- `PrepareExit` now stops automation admission before recorder/session drain. Queued runs are interrupted, the active run is cancelled, and the database closes only after the shared exit deadline.

Deferred:

- Real Windows tray icon/menu/Shell-restart and native confirmation screenshots were not run because the user explicitly deferred real test-client acceptance.
- Race detector still cannot load on this Windows toolchain (`0xc0000139`).

## Step 11 — integration and failure coverage

| Field | Value |
| --- | --- |
| Commit | `test(automation): cover scheduling recovery and desktop integration` |
| Integration | `TestAutomationIntegrationScheduleReceiptLinkAndRestart` → PASS |
| Frontend flow | `automation-flow.test.tsx` → PASS |
| Requirement matrix | `docs/automation/validation/requirements-matrix.md` |

The integration test proves: save does not execute, fake-clock advance starts one unfocused run, receipt and terminal facts are persisted, `session_link` points to the task-owned workspace/session, and a service restart before the next occurrence neither replays the terminal round nor creates a duplicate submission.

Failure injection is covered by existing focused tests rather than a second test implementation:

- invalid/missing model binding and directory disappearance: `TestAutomationWorkspaceMissing`, reuse/config drift tests;
- accepted-but-unrecorded receipt: the receipt-write phase of `TestAutomationRunnerFastTerminal`;
- duplicate/late terminal: `TestAutomationReplay...` service tests and `TestAutomationStoppedTurnEndNotSuccess`;
- busy queue and 60-minute timeout: `TestServiceMissedAndBusy`, `TestServiceRunTimeout`;
- identity/context changes: `TestAutomationCommandsScopeAndRevision`, `TestAutomationContextLateResponse`, `TestAutomationContextLateEvent`.

Final checks:

| Check | Result |
| --- | --- |
| `go run ./tools/protogen --check` | PASS |
| `go build ./...` | PASS |
| `go test ./internal/... ./cmd/runcode/...` | PASS |
| `go -C cmd/runcode-server test ./...` / build | PASS |
| `go -C cmd/runcode-desktop test ./...` / build | PASS |
| Frontend typecheck/lint/Vitest/build | PASS; 96 files, 942 tests |
| `golangci-lint run --new-from-rev=a93bd42 ./...` | PASS, 0 issues |
| Full historical golangci run | 528 pre-existing repository findings; log retained |
| `go test -race ./internal/... ./cmd/runcode/...` | NOT RUN TO COMPLETION: Windows toolchain exits `0xc0000139` before tests |
| `go test -race` in `cmd/runcode-server` | Same toolchain load failure (`0xc0000139`) |

The user explicitly deferred real client acceptance after Step 9, so the Windows tray, cancellation/confirmation dialogs, Explorer restart, sleep/wake and real model round-trip remain release-time manual checks. Automated tests do not claim those as visually accepted.

## Step 11A — task interaction and close/exit refinement

Date: 2026-11-19. Plan: `C:\Users\yyzxc\Desktop\自动任务优化\自动任务交互优化执行方案.md`.

| Field | Value |
| --- | --- |
| Schema | v2 adds soft deletion, run trigger and manual-run idempotency |
| Protocol | `RunAutomationTaskNow`, `DeleteAutomationTask`, active/linked run fields and `currentWorkspace` |
| Frontend | folded workspace picker, fixed detail header/footer, run-now/delete menu, editable action feedback |
| Lifecycle | X chooser, safe cancel, hide/exit split, forced-quit fallback |
| Verification | root Go tests with `-p 1`, desktop module tests/build, 953 frontend tests, build, typecheck, lint, protogen and new-code lint |

Behavior completed:

- The default workspace comes from the workspace saved in settings. The picker has no search input; it shows the selected path as a folded row, puts current plus MRU directories in a portal menu, and keeps “浏览其他目录...” fixed at the bottom. A browsed directory becomes selectable immediately without calling session APIs.
- “创建时配置” was removed from create and detail views. Tasks continue to use the model, connection, tenant and permission mode already saved by the user.
- Task detail has a fixed header and footer, an independently scrolling body, a title status line, circular play/pause control, three-box-plus more icon, immediate-run and delete actions, and a linked-session row.
- Successful run-status events now refresh the selected task with run ordering that remains correct when a new run restarts at `runRevision=1`. Action errors remain visible on either tab.
- Manual execution is idempotent by task plus `clientRequestId`, works while the task is paused, leaves task state and `nextRunAtUTC` unchanged, and uses the same queue/session/history pipeline.
- Delete is a soft delete. It removes the task from lists and due claims, rejects edits, pause/resume and later runs, and preserves runs plus session links. Deleted-task tests also prove owner isolation and idempotent manual runs.
- Every main-window X invokes the native chooser. Cancel keeps the visible window and scheduler unchanged; hide preserves the tray process; direct exit uses the same hold, bounded cleanup and Quit path as tray Exit. The active-run warning takes precedence.
- The title-bar close button re-enables after the chooser is created or cancelled, so restoring from the tray cannot leave X permanently disabled. A host-level two-second `os.Exit(0)` fallback runs only if Wails `Quit` returned but the Windows process remained alive.

Verification evidence is stored as `docs/automation/validation/step11a-*`. The first parallel root run hit the existing `internal/localusage` deadline while CPU-heavy suites overlapped; the test passed alone and the serial full root suite then passed. Real Windows tray/dialog acceptance was explicitly deferred by the user, so it is not marked visually accepted.
