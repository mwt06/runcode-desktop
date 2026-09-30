import { useChatDrafts } from '@/session/use-chat-drafts'
import { useQuestionRetry } from '@/session/use-question-retry'
// App 只做两件事：把各个 hook 接起来（会话 / 对话 / 权限队列 / 预览栏 / 文件），
// 以及按当前视图摆放 shell 组件。任何具体逻辑都不在这里——需要改行为时去
// session/ 下对应的 hook，需要改样子去 shell/ 或各页面。
import { useEffect, useRef, useState } from 'react'
import { usePersistentBool } from '@/hooks/use-persistent-state'
import { errText, loadConfig, passportLogout, type SessionInfo } from '@/core/bridge'
import { passportDisplayName } from '@/core/passport-account'
import { BRAND } from '@/core/brand'
import { isPreviewable, toWorkspaceRel } from '@/preview/classify'
import { useToast } from '@/session/use-toast'
import { usePermissionQueue } from '@/session/use-permission-queue'
import { useAskpass } from '@/session/use-askpass'
import { useWorkspaceFiles } from '@/session/use-workspace-files'
import { usePreviewPanel } from '@/session/use-preview-panel'
import { useConversation } from '@/session/use-conversation'
import { usePlan } from '@/session/use-plan'
import { useAutoPreview } from '@/session/use-auto-preview'
import { useSession } from '@/session/use-session'
import { useRecorder } from '@/session/use-recorder'
import { usePassportStatus } from '@/session/use-passport'
import { useUpdate } from '@/session/use-update'
import { TitleBar } from '@/shell/title-bar'
import { StatusBar } from '@/shell/status-bar'
import { ChatPane } from '@/shell/chat-pane'
import { PermissionModal } from '@/shell/permission-modal'
import { AskpassModal } from '@/shell/askpass-modal'
import { PreviewSide } from '@/shell/preview-side'
import { Sidebar } from '@/shell/sidebar'
import { Composer } from '@/composer'
import { type BuiltinAction } from '@/composer/scenario-bar'
import { InstallOverlay } from '@/composer/install-overlay'
import { LiveRecorderCard } from '@/chat/recorder-card'
import { useScenarioActions } from '@/session/use-scenario-actions'
import { useRecordingMinutes } from '@/session/use-recording-minutes'
import { show as showRecorderWindow } from '@/recorder/window-api'
import { PluginsPage } from '@/pages/plugins'
import { MarketPage } from '@/pages/market'
import { PermissionsPage } from '@/pages/permissions'
import { MemoryPage } from '@/pages/memory'
import { SettingsPage } from '@/pages/settings'
import { StartForm } from '@/pages/start'

export type View = 'chat' | 'settings' | 'plugins' | 'market' | 'permissions' | 'memory'

export default function App() {
  const [view, setView] = useState<View>('chat')
  // 输入草稿按会话存：在 A 里写了一半切到 B，那半句不该跟着过去。
  const taRef = useRef<HTMLTextAreaElement>(null)
  // 侧栏折叠态：上提到这里，让折叠开关能放到主栏顶部状态条（「空闲」前），
  // 而侧栏本身按此 prop 变宽窄。
  const [sidebarCollapsed, toggleSidebar] = usePersistentBool('sidebar.collapsed', false)

  // infoRef 是给事件回调读当前会话用的镜像：订阅只注册一次，闭包里不能直接读
  // info 这个 state。
  const infoRef = useRef<SessionInfo | null>(null)
  // focusedId 是"当前看得见的是哪条会话"。对话状态与授权队列都按会话存一份，
  // 渲染只取这一条，所以它必须是**响应式的值**而不是 ref——ref 变了不会重渲染。
  //
  // 它比 session.info 慢一拍（在下面那个 effect 里同步），这一拍里显示的是上一条
  // 会话的状态。与改动前一致：那时的状态本来也要等 applyResumed/reset 才换掉。
  const [focusedId, setFocusedId] = useState('')

  const drafts = useChatDrafts(focusedId)
  const { input, setInput } = drafts
  const toast = useToast()
  const permissions = usePermissionQueue(focusedId)
  // sudo 密码框：请求可能来自任何一条会话（不只是当前聚焦的），所以挂在最外层。
  const askpass = useAskpass()
  const workspace = useWorkspaceFiles(() => infoRef.current?.cwd ?? '')
  const preview = usePreviewPanel(focusedId)
  // 登录用户的通行证状态：欢迎语称呼（经 passportDisplayName）与侧栏用户区
  // （头像/用户名/退出登录）共用同一份订阅。未登录时名字为空串、头像为 undefined。
  const passport = usePassportStatus()
  const userName = passportDisplayName(passport)

  // 录音纪要。状态在 Go 侧，这里只订阅——录音窗是另一个 WebView，两边各跑一份
  // 这个 hook，看到的是同一份事实。
  const recorder = useRecorder()
  // 版本更新。状态同样在 Go 侧（启动几秒后自动查一次），这里只订阅——设置页画
  // 详情，侧栏据此点一颗小红点。
  const update = useUpdate()
  const startRecording = () => {
    recorder
      .start()
      // 开录成功才把窗口叫出来：开不起来（没配服务地址、麦克风被占）时弹一个
      // 空窗口，比一条错误提示更难懂。
      .then(() => showRecorderWindow())
      .catch((e: unknown) => toast.show(errText(e)))
  }
  // 场景表里「录音纪要」那一类不是提示词，是客户端内置功能，点了直接开录。
  //
  // 品牌没开这个功能（BRAND.features.recorder）时一条都不交：场景栏据此把整个分类
  // 不画（见 visibleScenarios），而不是画一个点不动的按钮。
  const builtinScenarios: Record<string, BuiltinAction> = BRAND.features.recorder
    ? {
      recorder: {
        title: recorder.recording ? '正在录音' : '开始录音，同时录下麦克风与系统声音',
        disabled: recorder.recording || recorder.paused,
        onPick: startRecording,
      },
    }
    : {}

  const { installing, pickScenario } = useScenarioActions({
    sessionId: focusedId, input, setInput, textarea: taRef, notify: toast.show,
  })

  const conversation = useConversation({
    focusedId,
    infoRef,
    permissions,
    showToast: toast.show,
    onFilesChanged: workspace.refresh,
    // 模型调用 open_preview 时给的是绝对路径（或工作区相对路径），统一换算后再开。
    onOpenPreview: (p) => preview.openFile(toWorkspaceRel(p, infoRef.current?.cwd ?? '')),
  })


  const { sendRecordingRequest } = useRecordingMinutes({
    sessionId: focusedId, recording: recorder.info, send: async (...args) => { await conversation.send(...args) },
    pushRecording: conversation.pushRecording, notify: toast.show,
  })
  const session = useSession({
    busy: conversation.busy,
    conversation,
    showToast: toast.show,
    onEnterChat: () => setView('chat'),
  })
  const questionRetry = useQuestionRetry({
    sessionId: focusedId,
    blocked: conversation.busy || conversation.compacting || session.switching,
    drafts, conversation, session, textarea: taRef, notify: toast.show,
  })
  useEffect(() => {
    infoRef.current = session.info
    setFocusedId(session.info?.sessionId ?? '')
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

  // 会话列表的行由三处拼起来：后端的 OpenSessions（是谁、哪条聚焦）、对话状态
  // （有没有回合在跑）、授权队列（有几个在等）。刻意不让后端一次性给全——
  // 运行状态与待审批数在前端本来就是实时的，从后端再取一份就会有两个版本。
  // waitingElsewhere 是**别的**会话里有请求在等人应答的那些 id。当前这条会话的
  // 请求已经以弹窗的形式挡在眼前，不必再提醒一遍。
  const waitingElsewhere = Object.keys(permissions.waiting).filter(
    (id) => id !== session.info?.sessionId && (permissions.waiting[id] ?? 0) > 0,
  )

  // 标题三级回落：自动标题（回合结束后模型生成）> 最近一次提问（临时顶上，
  // 一按发送就有）> 「新对话」。中间这一级是并行时认人的主力——自动标题来得晚，
  // 没有它整栏都长一个样。
  const openRows = session.openList.map((s) => ({
    id: s.sessionId,
    title: session.titles[s.sessionId] || conversation.lastUserBySession[s.sessionId] || '新对话',
    running: !!conversation.busyBySession[s.sessionId],
    waiting: permissions.waiting[s.sessionId] ?? 0,
    focused: s.sessionId === session.info?.sessionId,
    workspace: s.workspace,
  }))

  return (
    <div className="flex flex-col h-screen">
      <TitleBar />
      <div className="flex flex-1 min-h-0">
        <Sidebar
          collapsed={sidebarCollapsed}
          hasUpdate={update.hasUpdate}
          recents={session.recents}
          openSessions={openRows}
          onFocusSession={(id) => {
            if (session.switching) return
            setView('chat')
            void session.focusOn(id)
          }}
          onCloseSession={(id) => {
            if (session.switching) return
            // 这条会话的界面态跟着它一起走：预览标签（存着它的编辑快照 id）、
            // 输入草稿。对话状态由 closeOne 里的 dropSession 负责。
            preview.dropSession(id)
            drafts.update(id, () => ({ text: '', attachments: [] }))
            void session.closeOne(id)
          }}
          currentId={session.info?.sessionId}
          cwd={session.info?.cwd}
          recentWorkspaces={session.initialReq?.recentWorkspaces ?? []}
          onPickWorkspace={(dir) => { if (!session.switching) void session.openWorkspace(dir) }}
          onSwitchWorkspace={() => { if (!session.switching) void session.pickWorkspaceAndOpen() }}
          onDelete={session.deleteRecent}
          view={view}
          onNav={setView}
          onNew={() => {
            if (session.switching) return
            setView('chat')
            // 已经有会话就**加开一条**（并行），否则走替换式打开。
            //
            // 判据是 session.info 而不是 openList 的长度：openList 是异步回读来的，
            // 会滞后。曾经按它判断，结果首个会话还没进列表时点「新建对话」走成了
            // 替换式打开——正在跑的那个回合当场被关掉。info 永远是当前的。
            void (session.info ? session.openAnother() : session.newChat())
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
            waitingElsewhere={waitingElsewhere.length}
            onGoWaiting={() => {
              const first = waitingElsewhere[0]
              if (!first || session.switching) return
              setView("chat")
              void session.focusOn(first)
            }}
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
              update={update}
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
          ) : view === 'market' ? (
            <MarketPage />
          ) : view === 'permissions' ? (
            <PermissionsPage mode={session.info?.permissionMode} onPickMode={session.pickMode} />
          ) : view === 'memory' ? (
            <MemoryPage />
          ) : (
            <>
              <ChatPane
                onRetryQuestion={(id) => void questionRetry.retry(id)}
                onEditQuestion={(id) => void questionRetry.edit(id)}
                questionActionsDisabled={questionRetry.disabled}
                blocks={conversation.blocks}
                busy={conversation.busy}
                cwd={session.info?.cwd}
                userName={userName}
                plan={conversation.plan}
                planOpen={conversation.planOpen}
                onPlanToggle={conversation.setPlanOpen}
                planning={planning}
                harmAllows={conversation.harmAllows}
                oaBlocked={conversation.oaBlocked}
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
                recorderCard={<LiveRecorderCard rec={recorder} onOpenWindow={() => void showRecorderWindow()} />}
                onGenerateMinutes={(m) => void sendRecordingRequest(m, 'doc')}
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
                key={focusedId}
                attachments={drafts.attachments}
                setAttachments={drafts.setAttachments}
                retry={questionRetry.editing}
                retryDisabled={questionRetry.disabled}
                submitting={questionRetry.pending || session.switching || conversation.compacting}
                onCancelRetry={questionRetry.cancel}
                onRemoveOriginal={questionRetry.removeImage}
                input={input}
                onInputChange={setInput}
                taRef={taRef}
                chatScrollRef={conversation.scrollRef}
                busy={conversation.busy}
                toast={toast.text}
                info={session.info}
                files={workspace.files}
                sessionId={session.info?.sessionId}
                onRefreshFiles={workspace.refresh}
                onSend={questionRetry.send}
                onNotify={toast.show}
                onStop={conversation.stop}
                onToggleMode={session.toggleMode}
                onTogglePlan={() => void session.togglePlan()}
                onChooseReasoning={(s) => void session.chooseReasoning(s)}
                onChooseThinking={(t) => void session.chooseThinking(t)}
                onPickModel={session.pickModel}
                builtinScenarios={builtinScenarios}
                onPickScenario={(sc) => void pickScenario(sc)}
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
      {installing && <InstallOverlay state={installing} />}
      {askpass.current && (
        <AskpassModal
          req={askpass.current}
          remaining={askpass.remaining}
          onAnswer={(pw) => askpass.answer(askpass.current!.id, pw)}
          onCancel={() => askpass.cancel(askpass.current!.id)}
        />
      )}
    </div>
  )
}
