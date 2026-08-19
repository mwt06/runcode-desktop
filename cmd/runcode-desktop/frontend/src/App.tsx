// App 只做两件事：把各个 hook 接起来（会话 / 对话 / 权限队列 / 预览栏 / 文件），
// 以及按当前视图摆放 shell 组件。任何具体逻辑都不在这里——需要改行为时去
// session/ 下对应的 hook，需要改样子去 shell/ 或各页面。
import { useCallback, useEffect, useRef, useState } from 'react'
import { usePersistentBool } from '@/hooks/use-persistent-state'
import { errText, listSkills, loadConfig, passportLogout, readRecordingTranscript, type RecordingInfo, type SessionInfo } from '@/core/bridge'
import { passportDisplayName } from '@/core/passport-account'
import { isPreviewable, toWorkspaceRel } from '@/preview/classify'
import { useToast } from '@/session/use-toast'
import { usePermissionQueue } from '@/session/use-permission-queue'
import { useWorkspaceFiles } from '@/session/use-workspace-files'
import { usePreviewPanel } from '@/session/use-preview-panel'
import { useConversation } from '@/session/use-conversation'
import { usePlan } from '@/session/use-plan'
import { useAutoPreview } from '@/session/use-auto-preview'
import { useSession } from '@/session/use-session'
import { useRecorder } from '@/session/use-recorder'
import { usePassportStatus } from '@/session/use-passport'
import { TitleBar } from '@/shell/title-bar'
import { StatusBar } from '@/shell/status-bar'
import { ChatPane } from '@/shell/chat-pane'
import { PermissionModal } from '@/shell/permission-modal'
import { PreviewSide } from '@/shell/preview-side'
import { Sidebar } from '@/shell/sidebar'
import { Composer } from '@/composer'
import { type QuickSkill } from '@/composer/quick-skills'
import { RecorderCard } from '@/chat/recorder-card'
import { buildMinutesPrompt, minutesFileName, pickMinutesSkill } from '@/recorder/minutes'
import { show as showRecorderWindow } from '@/recorder/window-api'
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
  const workspace = useWorkspaceFiles(() => infoRef.current?.cwd ?? '')
  const preview = usePreviewPanel()
  // 登录用户的通行证状态：欢迎语称呼（经 passportDisplayName）与侧栏用户区
  // （头像/用户名/退出登录）共用同一份订阅。未登录时名字为空串、头像为 undefined。
  const passport = usePassportStatus()
  const userName = passportDisplayName(passport)

  // 录音纪要。状态在 Go 侧，这里只订阅——录音窗是另一个 WebView，两边各跑一份
  // 这个 hook，看到的是同一份事实。
  const recorder = useRecorder()
  const startRecording = () => {
    recorder
      .start()
      // 开录成功才把窗口叫出来：开不起来（没配服务地址、麦克风被占）时弹一个
      // 空窗口，比一条错误提示更难懂。
      .then(() => showRecorderWindow())
      .catch((e: unknown) => toast.show(errText(e)))
  }
  const quickSkills: QuickSkill[] = [
    {
      id: 'recorder',
      label: '录音纪要',
      icon: 'mic',
      title: recorder.recording ? '正在录音' : '开始录音，同时录下麦克风与系统声音',
      disabled: recorder.recording || recorder.paused,
      onPick: startRecording,
    },
  ]

  const conversation = useConversation({
    infoRef,
    permissions,
    showToast: toast.show,
    onFilesChanged: workspace.refresh,
    // 模型调用 open_preview 时给的是绝对路径（或工作区相对路径），统一换算后再开。
    onOpenPreview: (p) => preview.openFile(toWorkspaceRel(p, infoRef.current?.cwd ?? '')),
  })


  // 会后纪要：录音一结束就把转写交给模型整理。
  //
  // 走的是当前这条对话，而不是另起一个后台任务——设计稿里纪要就出现在对话流里，
  // 用户接着能直接追问「第三条待办是谁负责的」，那要求它和会话共享上下文。
  const minutesFired = useRef('')
  const generateMinutes = useCallback(async (rec: RecordingInfo) => {
    if (!rec.id) return
    if (!infoRef.current) {
      toast.show('还没有进行中的对话，无法生成纪要')
      return
    }
    try {
      const text = await readRecordingTranscript(rec.id)
      if (!text.trim()) {
        toast.show('这场录音没有转写文字，生成不了纪要')
        return
      }
      const list = await listSkills().catch(() => null)
      const skill = pickMinutesSkill((list?.skills ?? []).map((s) => s.name))
      await conversation.send(buildMinutesPrompt({
        info: rec, transcript: text, skill, outPath: minutesFileName(rec),
      }))
    } catch (e) {
      toast.show(errText(e))
    }
  }, [conversation, toast])

  // 自动触发一次。用 id 记名而不是布尔量：连着录两场时第二场也要触发，而同一场
  // 不能因为别的状态事件再触发一遍——那是一次白花钱的重复调用。
  useEffect(() => {
    const rec = recorder.info
    if (!rec || rec.state !== 'stopped' || !rec.id || !rec.transcript) return
    if (minutesFired.current === rec.id) return
    minutesFired.current = rec.id
    void generateMinutes(rec)
  }, [recorder.info, generateMinutes])
  const session = useSession({
    busy: conversation.busy,
    conversation,
    showToast: toast.show,
    onEnterChat: () => setView('chat'),
    // 换工作区 → 关掉所有预览标签(旧工作区的相对路径与编辑快照都失效了)。
    onWorkspaceChanged: preview.closeAll,
  })
  useEffect(() => {
    infoRef.current = session.info
  }, [session.info])

  // 阶段化计划模式（需求理解 → 方案设计 → 方案审查 → 用户审批）。确认方案后要发的
  // 那条执行指令由后端拼好，这里原样走 conversation.send——busy、用户气泡、回合生命
  // 周期因此完全复用普通消息的链路，不另起一套。
  const planning = usePlan({
    sessionId: session.info?.sessionId,
    onSend: (text) => void conversation.send(text),
    onApproved: session.setInfo,
    showToast: toast.show,
  })

  // Workspace file list for the composer picker (#), the file browser, and reply
  // artifact matching — reloaded per session。把 reload 解出来做依赖:它是
  // useCallback([]) 的稳定引用,而 workspace 对象每次渲染都是新的。
  const reloadFiles = workspace.reload
  useEffect(() => {
    if (session.started) reloadFiles()
  }, [session.started, session.info?.sessionId, reloadFiles])

  useAutoPreview({
    busy: conversation.busy,
    blocks: conversation.blocks,
    cwd: session.info?.cwd ?? '',
    enabled: preview.autoOpen,
    opens: preview.opens,
    open: preview.openFile,
  })

  // 退出登录：先清本地通行证令牌，再把界面退回首屏。登出后 StartForm 重新校验登录态,
  // 自然落到登录门(除非开了免登录)——满足"退出登录须回登录页"。清令牌失败也照样
  // 退回首屏,由登录门给出下一步。
  const logout = async () => {
    try { await passportLogout() } catch { /* 清令牌失败也退回首屏,登录门兜底 */ }
    session.returnToStart()
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
          userName={userName}
          avatar={passport?.avatar}
          onLogout={() => void logout()}
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
              busy={conversation.busy}
              onSwitchModel={session.pickModel}
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
                userName={userName}
                plan={conversation.plan}
                planOpen={conversation.planOpen}
                onPlanToggle={conversation.setPlanOpen}
                planning={planning}
                harmAllows={conversation.harmAllows}
                revertedEdits={conversation.revertedEdits}
                files={workspace.files}
                tabs={preview.tabs}
                scrollRef={conversation.scrollRef}
                onScroll={conversation.onChatScroll}
                onAnswer={(text) => void conversation.send(text)}
                onOpenFile={preview.openFile}
                onReviewEdit={preview.openDiff}
                onUndoEdit={(id) => void conversation.undo(id)}
                resolveFile={workspace.resolve}
                recorderCard={<RecorderCard rec={recorder} onOpenWindow={() => void showRecorderWindow()} onGenerateMinutes={(r) => void generateMinutes(r)} />}
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
                quickSkills={quickSkills}
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
