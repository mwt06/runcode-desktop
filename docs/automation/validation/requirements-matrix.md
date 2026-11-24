# Automation requirement evidence

Date: 2026-11-19. Branch: `codex/automation-tasks-1118`.

| ID | Evidence |
| --- | --- |
| R01 | `more-menu.test.tsx`, sidebar order and automation entry; no view/session side effect. |
| R02 | `automation-state.test.ts`, `workspace-dialog.test.tsx`, manager tabs/search/list/create. |
| R03 | `task-form.test.tsx`, `draft.test.ts`, create form and read-only model/permission summary. |
| R04 | `workspace-picker.test.tsx`; `TestAutomationWorkspacePickerContract`; “浏览其他目录...” delegates to `PickWorkspaceFolder`. |
| R05 | `draft.test.ts` and `task-form.test.tsx` cover exactly seven schedule modes and reject custom/cron fields. |
| R06 | `TestServicePauseKeepsCurrentRun`, `TestServiceResumeFutureOnly`, Step 9 pause/resume UI and filter-state tests. |
| R07 | `TestAutomationReplayLinkAndWorkspace`, session automation origin mapping and sidebar clock tests. |
| R08 | `automation-run.test.ts`, `automation-turns.test.ts`, `execution-card.test.tsx`, `step6-execution-card.jpg`. |
| R09 | `window_lifecycle_test.go`, `automation_exit_test.go`, Windows tray host build; real tray deferred by user. |
| R10 | `TestExitConfirmationCounts`, `TestExitCancelKeepsScheduler`, `TestExitDoubleClickOneDialog`, `TestExitAdmissionRace`, `TestPrepareExitStopsAutomationBeforeSessionDrain`; shared `ExitTimeout`. |
| R11 | `TestAutomationDefaultsUsesSettingsWorkspace`, `draft.test.ts`; the saved settings workspace is the default and current MRU entry. |
| R12 | `workspace-picker.test.tsx`; no search field, folded trigger, portal list, fixed browse row and immediate local insertion. |
| R13 | `task-form.test.tsx`, `task-detail.test.tsx`; no “创建时配置” and no task-level model/permission override. |
| R14 | `task-detail.test.tsx`; fixed footer, independent body scroll, status line, circular pause/play, three-box-plus icon and linked session. |
| R15 | `TestServiceRunNowDoesNotMoveScheduleOrEnablePausedTask`, `TestStoreManualRunIsIdempotentAndDoesNotMoveSchedule`, `TestAutomationManualRunIdempotencyAndSoftDelete`, `use-automation.test.tsx`. |
| R16 | `TestStoreSoftDeletePreservesRunsAndLinks`, `TestAutomationManualRunIdempotencyAndSoftDelete`, `automation-state.test.ts`; soft delete, owner isolation and preserved history. |
| R17 | `automation-state.test.ts`; same-revision task snapshots merge by run identity/schedule and advanced run revision without dropping a new run. |
| R18 | `TitleBar` close lifecycle tests plus `WindowLifecycle` tests; cancel keeps the window visible and the title-bar X re-enables after the chooser. |
| R19 | `TestMainCloseActiveRunWarnsAndCanExit`, `TestConfirmedExitQuitsAfterCleanupTimeout`, `TestQuitFallbackWhenEventLoopDoesNotExit`; active warning, timeout Quit and Windows host fallback. |

The Windows visual checks for the tray icon, Shell restart, native confirmation and real model execution are intentionally not marked complete because real test-client acceptance was deferred by the user. Automated evidence is otherwise green. Step 11A logs are stored as `step11a-*` beside this matrix.
