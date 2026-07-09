# 修复"打开方式"动作（跨平台）+ 去掉卡片色带

日期：2026-07-09
状态：已批准设计，待写实现计划

## 背景

桌面产物卡片的"打开方式"三项——系统默认打开 / 在文件夹中显示 / 复制路径——用户反馈都失效。根因：为堵住 `cmd /c start` 的命令注入 RCE，`OpenExternal` 改用了 `rundll32 url.dll,FileProtocolHandler`，但它对本地文件打开并不可靠。程序需在 **Mac / Windows / Linux** 三平台运行，修复必须跨平台且保持"不走 shell"（不重新引入注入）。同时用户要求卡片去掉那条彩色竖线。

本 spec 只覆盖这两项小改动。识别文件卡片改走正则、给模型加 `preview` 工具——设计已定但**留到下一轮**（见"已定但延后")。

## 范围

**做**：
1. 跨平台修好三个"打开方式"动作（全部不走 shell）。
2. 去掉 `ArtifactCard` 的彩色左竖线（保留彩色类型图标）。

**不做（下一轮）**：正则识别文件卡片、`preview` 模型工具（见文末）。

## 设计

### #1 三个动作跨平台修复（`internal/desktop/open.go`）

保持单文件 `runtime.GOOS` switch（无 build tag、无新依赖），全部经 `exec.Command`（直接调可执行文件，非 shell —— 文件名里的 `&`/`^` 等是惰性 argv，注入面为零），并沿用现有的越界拒绝（`resolveWithinWorkspace`，在启动进程前校验）。

- **系统默认打开**（替换失效的 rundll32）：
  - Windows：`explorer <abs>`（explorer.exe 直接打开文件即用默认程序）
  - macOS：`open <abs>`
  - Linux：`xdg-open <abs>`
- **在文件夹中显示**：
  - Windows：`explorer /select,<abs>`（单个 argv token）
  - macOS：`open -R <abs>`
  - Linux：`xdg-open <父目录>`（无跨发行版的"选中"动词，退化为打开所在目录）
- **复制路径**：`ResolveArtifactPath(relPath)` 返回工作区内绝对路径；前端 `copyText` 先试 `window.runtime.ClipboardSetText`，失败回退 `navigator.clipboard.writeText`。

保留现有"不走 shell"回归测试 `TestOpenCommandDoesNotUseShell`：`open`/`reveal` 命令的可执行名不得是 `cmd*`/`powershell*`/`pwsh`/`sh`/`bash`；`explorer`/`open`/`xdg-open` 均通过。

> 后备（暂不做）：若 Windows `explorer <path>` 对某些类型仍不稳，改用 `ShellExecute`（需 build tag + `golang.org/x/sys/windows`）。先用简单可靠的 explorer。

### #3 去掉彩色竖线（`cmd/runcode-desktop/frontend/src/artifact-card.tsx`）

`ArtifactCard` 去掉左侧色带：删掉 `style={{ borderLeftColor: accent, borderLeftWidth: 3 }}` 及对应的 `pl-3`/`border-left` 处理，换回普通四边描边卡（`border border-line2 rounded-lg`）。**保留**彩色类型图标（`kindIcon` + `style={{ color: accent }}`）——那不是竖线。`kindAccent` 若仅剩图标用则保留；预览标签/头部的类型色仍延续（Task 6 不动）。

## 数据流 / 错误处理

- 三动作前置 `resolveWithinWorkspace` 越界拒绝不变（fail-closed）。
- `open`/`reveal` 用 `.Start()`（不等待）——explorer 退出码恒为 1 等怪癖不影响。
- 复制失败（剪贴板不可用）静默兜底，不崩。
- 去色带纯样式改动，无行为变化。

## 测试计划

- **Go**：`TestOpenCommandDoesNotUseShell` 更新/保留（覆盖 open + reveal 两个命令构造器都非 shell、路径为单一惰性 argv）；越界拒绝测试不变（`TestOpenBindingsRejectEscape`）。OS 启动副作用不单测。
- **前端**：去色带无逻辑、构建校验；现有 vitest 保持绿。
- **手动**：Windows 上"系统默认打开 / 在文件夹显示 / 复制路径"三项都生效；卡片无彩色竖线、彩色图标仍在。

## 落点

- `internal/desktop/open.go`（open 用 explorer/open/xdg-open；reveal 保持；命令构造器可测）、`internal/desktop/open_test.go`（no-shell 测试覆盖 open+reveal）。
- `cmd/runcode-desktop/frontend/src/artifact-card.tsx`（去色带）。

## 已定但延后（下一轮，勿丢）

- **#2 卡片来源改为"只用正则"**：扫 AI 回复正文 + 工具输出里"像路径"的 token（带已知扩展名、含 `/`/`\` 或纯文件名），只保留工作区文件列表里真实存在的，去重成卡片；**不再用 Write/Edit 工具事件生卡**。
- **#4 `preview` 模型工具**：新增 agent 工具 `preview(path)`（限工作区内、可预览），经工具事件 `data` 通道通知桌面前端开成标签；**仅桌面注册，CLI 不暴露**；工具描述引导模型在生成文档/网站(H5)后主动调用。
