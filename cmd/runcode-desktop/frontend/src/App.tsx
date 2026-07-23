// App 只做两件事：把各个 hook 接起来（会话 / 对话 / 权限队列 / 预览栏 / 文件），
// 以及按当前视图摆放 shell 组件。任何具体逻辑都不在这里——需要改行为时去
// session/ 下对应的 hook，需要改样子去 shell/ 或各页面。
import { useEffect, useRef, useState } from 'react'
import { usePersistentBool } from '@/hooks/use-persistent-state'
import { loadConfig, type SessionInfo } from '@/core/bridge'
import { isPreviewable, toWorkspaceRel } from '@/preview/classify'
import { useToast } from '@/session/use-toast'
import { usePermissionQueue } from '@/session/use-permission-queue'
import { useWorkspaceFiles } from '@/session/use-workspace-files'
import { usePreviewPanel } from '@/session/use-preview-panel'
import { useConversation } from '@/session/use-conversation'
import { useAutoPreview } from '@/session/use-auto-preview'
import { useSession } from '@/session/use-session'
import { TitleBar } from '@/shell/title-bar'
import { StatusBar } from '@/shell/status-bar'
import { ChatPane } from '@/shell/chat-pane'
import { PermissionModal } from '@/shell/permission-modal'
import { PreviewSide } from '@/shell/preview-side'
import { Sidebar } from '@/shell/sidebar'
import { Composer } from '@/composer'
import { PluginsPage } from '@/pages/plugins'
import { PermissionsPage } from '@/pages/permissions'
import { MemoryPage } from '@/pages/memory'
import { SettingsPage } from '@/pages/settings'
import { StartForm } from '@/pages/start'

export type View = 'chat' | 'settings' | 'plugins' | 'permissions' | 'memory'

export default function App() {
  const [view, setView] = useState<View>('chat')
  const [input, setInput] = useState('')
  const taRef = useRef<HTMLTextAreaElement>(null)
  // 侧栏折叠态：上提到这里，让折叠开关能放到主栏顶部状态条（「空闲」前），
  // 而侧栏本身按此 prop 变宽窄。
  const [sidebarCollapsed, toggleSidebar] = usePersistentBool('sidebar.collapsed', false)

  // infoRef 是给事件回调读当前会话用的镜像：订阅只注册一次，闭包里不能直接读
  // info 这个 state。
  const infoRef = useRef<SessionInfo | null>(null)

  const toast = useToast()
  const permissions = usePermissionQueue()
  const workspace = useWorkspaceFiles()
  const preview = usePreviewPanel()

  const conversation = useConversation({
    infoRef,
    permissions,
    showToast: toast.show,
    onFilesChanged: workspace.refresh,
    // 模型调用 open_preview 时给的是绝对路径（或工作区相对路径），统一换算后再开。
    onOpenPreview: (p) => preview.openFile(toWorkspaceRel(p, infoRef.current?.cwd ?? '')),
  })

  const session = useSession({
    busy: conversation.busy,
    conversation,
    showToast: toast.show,
    onEnterChat: () => setView('chat'),
  })
  useEffect(() => {
    infoRef.current = session.info
  }, [session.info])

  // Workspace file list for the composer picker (#), the file browser, and reply
  // artifact matching — reloaded per session.
  useEffect(() => {
    if (session.started) workspace.reload()
  }, [session.started, session.info?.sessionId])

  useAutoPreview({
    busy: conversation.busy,
    blocks: conversation.blocks,
    cwd: session.info?.cwd ?? '',
    enabled: preview.autoOpen,
    open: preview.openFile,
  })

  // executePlanAs 离开计划模式、切到选定的权限模式，再让模型按方案执行——
  // 唯一同时牵动会话与对话的动作，所以留在这里编排。
  const executePlanAs = async (mode: string) => {
    if (conversation.busy) return
    conversation.dismissPlanChoice()
    if (await session.leavePlanMode(mode)) {
      await conversation.send('计划已确认，请按上述方案开始执行。')
    }
  }

  if (!session.started) {
    return (
      <div className="flex flex-col h-screen">
        <TitleBar />
        {session.initialReq ? (
          <StartForm onStart={session.start} starting={session.starting} error={session.startError} initial={session.initialReq} />
        ) : (
          <div className="flex-1" />
        )}
      </div>
    )
  }

  const showPreview = view === 'chat' && (preview.tabs.length > 0 || preview.browseOpen)

  return (
    <div className="flex flex-col h-screen">
      <TitleBar />
      <div className="flex flex-1 min-h-0">
        <Sidebar
          collapsed={sidebarCollapsed}
          recents={session.recents}
          currentId={session.info?.sessionId}
          cwd={session.info?.cwd}
          recentWorkspaces={session.initialReq?.recentWorkspaces ?? []}
          onPickWorkspace={(dir) => { if (!session.switching) void session.switchToWorkspace(dir) }}
          onSwitchWorkspace={() => { if (!session.switching) void session.pickWorkspaceAndSwitch() }}
          onDelete={session.deleteRecent}
          view={view}
          onNav={setView}
          onNew={() => {
            if (session.switching) return
            setView('chat')
            void session.newChat()
          }}
          onResume={(id) => {
            if (session.switching) return
            setView('chat')
            void session.openRecent(id)
          }}
        />

        <main className="flex-1 flex flex-col min-w-0 min-h-0 bg-surface">
          <StatusBar
            busy={conversation.busy}
            sidebarCollapsed={sidebarCollapsed}
            onToggleSidebar={toggleSidebar}
            ctxTokens={conversation.ctxTokens}
            ctxBudget={session.info?.maxContextTokens ?? 0}
            ctxEstimated={conversation.ctxEstimated}
            compacting={conversation.compacting}
            onCompact={() => void conversation.compact()}
            onTogglePreview={() => {
              preview.setBrowseOpen((v) => !v)
              // opening: refresh files
              if (!preview.browseOpen) workspace.refresh()
            }}
          />

          {view === 'settings' ? (
            <SettingsPage
              initial={session.initialReq ?? {}}
              info={session.info}
              onSaved={(i) => {
                session.setInfo(i)
                loadConfig().then((c) => session.setInitialReq(c ?? {})).catch(() => {})
              }}
            />
          ) : view === 'plugins' ? (
            <PluginsPage
              onUseSkill={(skillName) => {
                setView('chat')
                setInput((prev) => (prev.trim() ? prev + ' ' : '') + `请使用「${skillName}」技能完成：`)
                requestAnimationFrame(() => taRef.current?.focus())
              }}
              onUseAgent={(agentName) => {
                setView('chat')
                setInput((prev) => (prev.trim() ? prev + ' ' : '') + `请委派「${agentName}」子代理完成：`)
                requestAnimationFrame(() => taRef.current?.focus())
              }}
            />
          ) : view === 'permissions' ? (
            <PermissionsPage mode={session.info?.permissionMode} onPickMode={session.pickMode} />
          ) : view === 'memory' ? (
            <MemoryPage />
          ) : (
            <>
              <ChatPane
                blocks={conversation.blocks}
                busy={conversation.busy}
                cwd={session.info?.cwd}
                plan={conversation.plan}
                planOpen={conversation.planOpen}
                onPlanToggle={conversation.setPlanOpen}
                harmAllows={conversation.harmAllows}
                revertedEdits={conversation.revertedEdits}
                files={workspace.files}
                tabs={preview.tabs}
                scrollRef={conversation.scrollRef}
                onScroll={conversation.onChatScroll}
                onAnswer={(text) => void conversation.send(text)}
                onExecutePlan={(mode) => void executePlanAs(mode)}
                onDismissPlan={conversation.dismissPlanChoice}
                onOpenFile={preview.openFile}
                onReviewEdit={preview.openDiff}
                onUndoEdit={(id) => void conversation.undo(id)}
                resolveFile={workspace.resolve}
              />

              {permissions.pending && (
                <PermissionModal
                  req={permissions.pending}
                  onDecide={(d) => void permissions.decide(d)}
                  remaining={permissions.remaining}
                  onDenyRest={() => void permissions.denyRest()}
                />
              )}

              <Composer
                input={input}
                onInputChange={setInput}
                taRef={taRef}
                busy={conversation.busy}
                toast={toast.text}
                info={session.info}
                files={workspace.files}
                sessionId={session.info?.sessionId}
                onRefreshFiles={workspace.refresh}
                onSend={(text, attach) => void conversation.send(text, attach)}
                onStop={conversation.stop}
                onToggleMode={session.toggleMode}
                onTogglePlan={() => void session.togglePlan()}
                onChooseReasoning={(s) => void session.chooseReasoning(s)}
                onChooseThinking={(t) => void session.chooseThinking(t)}
                onPickModel={session.pickModel}
              />
            </>
          )}
        </main>

        {showPreview && (
          <PreviewSide
            tabs={preview.tabs}
            active={preview.active}
            baseURL={session.info?.previewBaseURL ?? ''}
            width={preview.width}
            dragHandlers={preview.dragHandlers}
            files={workspace.files}
            autoOpen={preview.autoOpen}
            onToggleAutoOpen={preview.toggleAutoOpen}
            onSelect={preview.setActive}
            onCloseTab={preview.close}
            onCloseAll={preview.closeAll}
            onCloseBrowser={() => preview.setBrowseOpen(false)}
            onPickFile={(p) => { if (isPreviewable(p)) preview.openFile(toWorkspaceRel(p, session.info?.cwd ?? '')) }}
          />
        )}
      </div>
    </div>
  )
}
