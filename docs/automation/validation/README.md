# Steps 6–9 visual validation

Date: 2026-11-19. Branch: `codex/automation-tasks-1118`.

The screenshots render the production tool card, task manager and task form components in a local Vite session. The temporary harness was removed after capture and is not part of the application.

| File | Evidence |
| --- | --- |
| `step6-execution-card.jpg` | Automation ExecutionCard is collapsed by default; its expanded row shows task, scheduled time, workspace, execution mode, prompt and output. |
| `step7-manager-1024x680.jpg` | Manager at 1024×680 with title, close button, four status tabs, search, create action and stable task rows. |
| `step7-manager-150pct.jpg` | Manager rendered at 150% CSS scale; content remains readable and internal scrolling is available. |
| `step8-form-1024x680.jpg` | Create drawer with both execution locations, recent-workspace selector, “浏览其他目录...” and the daily schedule fields. |
| `step8-form-150pct.jpg` | Form at 150% scale with title, close control and scrollable form content. |
| `step8-form-150pct-bottom.jpg` | Bottom of the 150% form after scrolling, with timezone summary and visible 取消／保存 controls. |

The seven schedule choices were also asserted in `task-form.test.tsx`: weekly, weekdays, daily, every N hours, every N minutes, monthly and once. No custom/cron option exists. Native folder selection is delegated to the existing `PickWorkspaceFolder`; `TestAutomationWorkspacePickerContract` verifies selected, cancelled and unsupported-dialog outcomes.

Step 10–11 regression logs are stored beside this file as `step11-*`. `requirements-matrix.md` maps R01–R19 to tests and the remaining Windows manual checks.

## Step 11A — interaction optimization evidence

Date: 2026-11-19. Branch: `codex/automation-tasks-1118`.

The `step11a-*` logs record the final automated evidence for the interaction refinement:

- root and nested-module Go tests/builds;
- frontend typecheck, lint, 953 Vitest tests and production build;
- protocol generation check and `golangci-lint --new-from-rev=f982567`.

The root suite is recorded with `-p 1`: one initial parallel run overlapped the complete desktop/frontend suites and an existing `internal/localusage` wall-clock test exceeded its deadline; that test passed alone and the serial full root suite passed. This does not replace the user-deferred Windows tray and native dialog acceptance.
