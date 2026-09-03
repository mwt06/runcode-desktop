// App 只做两件事：把各个 hook 接起来（会话 / 对话 / 权限队列 / 预览栏 / 文件），
// 以及按当前视图摆放 shell 组件。任何具体逻辑都不在这里——需要改行为时去
// session/ 下对应的 hook，需要改样子去 shell/ 或各页面。
import { useCallback, useEffect, useRef, useState } from 'react'
import { usePersistentBool } from '@/hooks/use-persistent-state'
import { Events, errText, installMarketSkill, listSkills, loadConfig, onEvent, passportLogout, readRecordingTranscript, skillMarket, type SessionInfo } from '@/core/bridge'
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
import { useUpdate } from '@/session/use-update'
import { TitleBar } from '@/shell/title-bar'
import { StatusBar } from '@/shell/status-bar'
import { ChatPane } from '@/shell/chat-pane'
import { PermissionModal } from '@/shell/permission-modal'
import { PreviewSide } from '@/shell/preview-side'
import { Sidebar } from '@/shell/sidebar'
import { Composer } from '@/composer'
import { type BuiltinAction } from '@/composer/scenario-bar'
import { InstallOverlay, type InstallState } from '@/composer/install-overlay'
import { applyScenario, skillHint, type Scenario } from '@/core/scenarios'
import { LiveRecorderCard } from '@/chat/recorder-card'
import { buildMinutesPrompt, minutesDisplayText, minutesFileName, pickMinutesSkill, recordingMark, type RecordingMark } from '@/recorder/minutes'
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
  const [drafts, setDrafts] = useState<Record<string, string>>({})
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

  const input = drafts[focusedId] ?? ''
  const setInput = (v: string | ((prev: string) => string)) =>
    setDrafts((d) => ({ ...d, [focusedId]: typeof v === 'function' ? v(d[focusedId] ?? '') : v }))
  // 正在安装的技能；null = 没在装。名字是点击那一刻就知道的，进度靠事件补。
  const [installing, setInstalling] = useState<InstallState | null>(null)

  // 安装进度：后端每走一步发一条（见 internal/desktop/skillmarket.go）。
  //
  // 订阅**常驻**，不是装的时候才挂：挂载订阅有一次异步往返，等点了再挂会漏掉最前面
  // 那几条——而 detail 阶段恰恰是最快的那一段，漏了就会看到进度环空转一下才动。
  useEffect(() => onEvent(Events.SkillInstall, (p) => {
    setInstalling((cur) => (cur ? { ...cur, progress: p } : cur))
  }), [])

  // 输入框当前内容的镜像。装技能要等网络，那期间用户可能接着打字——回来时拿闭包里
  // 捕获的旧 input 去算，会把他刚打的字整段顶掉。每次渲染都刷新，读的永远是最新值。
  const inputRef = useRef(input)
  inputRef.current = input

  const toast = useToast()
  const permissions = usePermissionQueue(focusedId)
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
  const builtinScenarios: Record<string, BuiltinAction> = {
    recorder: {
      title: recorder.recording ? '正在录音' : '开始录音，同时录下麦克风与系统声音',
      disabled: recorder.recording || recorder.paused,
      onPick: startRecording,
    },
  }

  // ensureSkill 保证场景关联的技能在本机可用，返回它到底能不能用。
  //
  // 顺序是「先看本地、再看市场」：绝大多数时候技能已经装好了，那一步是读磁盘、
  // 没有网络往返，也不需要登录。只有真缺的时候才去市场，而市场要登录、要选租户、
  // 还要 manageapi 授权——把这些前置条件套在每一次点场景上是不合适的。
  //
  // 装到**用户级**：内置场景在哪个项目里都该能用；装进项目级会跟着工作区走，还会
  // 往用户的仓库里塞 .runcode/skills。
  const ensureSkill = async (name: string): Promise<boolean> => {
    const local = await listSkills().catch(() => null)
    if ((local?.skills ?? []).some((s) => s.name === name)) return true

    setInstalling({ name, progress: null })
    try {
      const page = await skillMarket(false)
      const hit = (page.skills ?? []).find((s) => s.name === name)
      if (!hit) {
        // 场景表里写着这个技能，市场上却没有——多半是被下架或改名了。说清楚是
        // 「市场里没有」，别让人以为是网络问题去反复重试。
        toast.show(`市场里没有「${name}」技能，这次先不带它跑`)
        return false
      }
      await installMarketSkill(hit.id, 'user')
      toast.show(`已安装「${name}」技能`)
      return true
    } catch (e) {
      toast.show(errText(e))
      return false
    } finally {
      // 撤面板放在 finally：装失败时也得撤，否则用户会被一个永远转下去的遮罩困住。
      setInstalling(null)
    }
  }

  // 选了一个场景：确保关联技能可用，然后把提示词填进输入框，并**选中第一个
  // 【占位符】**，用户接着打字就替换掉它。
  //
  // 不直接发送——模型收到「帮我调研【XX话题】」只能反问，而那一问一答本可以省掉。
  // 填法（空则替换、有字则另起一段追加）见 applyScenario。
  //
  // 技能装不上（没登录、市场下架了、下载接口抽风）**不挡着填提示词**：那段提示词
  // 本身就是完整可用的，装了技能只是效果更好。这时候只是不点名技能——点名一个不
  // 存在的技能，模型会去调 Skill 工具然后报一句找不到，比不提它还糟。
  const pickScenario = async (sc: Scenario) => {
    const ready = sc.skill ? await ensureSkill(sc.skill) : false
    const { value, start, end } = applyScenario(inputRef.current, skillHint(ready ? sc.skill : '') + sc.prompt)
    setInput(value)
    // 等这次 setState 渲染完再动选区：现在设会被随后的受控更新冲掉。
    requestAnimationFrame(() => {
      const ta = taRef.current
      if (!ta) return
      ta.focus()
      ta.setSelectionRange(start, end)
    })
  }

  const conversation = useConversation({
    focusedId,
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
  const generateMinutes = useCallback(async (mark: RecordingMark) => {
    if (!mark.id) return
    if (!infoRef.current) {
      toast.show('还没有进行中的对话，无法生成纪要')
      return
    }
    try {
      const text = await readRecordingTranscript(mark.id)
      if (!text.trim()) {
        toast.show('这场录音没有转写文字，生成不了纪要')
        return
      }
      const list = await listSkills().catch(() => null)
      // 只把**启用着的**技能交给挑选：停用的技能引擎不会加载，点名它只会换来一句
      // 「找不到这个技能」，比不点名更糟。两个作用域任一停用即视为不可用，与插件页
      // 的「实际启用 = 两处都没关」是同一个判据。
      const usable = (list?.skills ?? []).filter((s) => !s.disabledUser && !s.disabledProject)
      const skill = pickMinutesSkill(usable.map((s) => s.name))
      // 对话里只显示一句话。整篇转写照旧发给模型，但几千字铺在对话流里会把用户
      // 自己的历史整个冲掉——设计稿那个位置本来就只有一句「录音纪要」。
      // 附了什么要说清楚，不能让人不知道自己刚把什么送了出去。
      await conversation.send(
        buildMinutesPrompt({ mark, transcript: text, skill, outPath: minutesFileName(mark) }),
        [],
        minutesDisplayText(mark.title),
      )
    } catch (e) {
      toast.show(errText(e))
    }
  }, [conversation, toast])

  // 录完自动走一次：先把卡片钉进对话（它属于这条对话，不是浮在界面上的东西），
  // 再发纪要请求。用 id 记名而不是布尔量：连着录两场时第二场也要触发，而同一场
  // 不能因为别的状态事件再触发一遍——那是一次白花钱的重复调用。
  useEffect(() => {
    const rec = recorder.info
    if (!rec || rec.state !== 'stopped' || !rec.id || !rec.transcript) return
    if (minutesFired.current === rec.id) return
    minutesFired.current = rec.id
    const mark = recordingMark(rec)
    conversation.pushRecording(mark)
    void generateMinutes(mark)
  }, [recorder.info, conversation, generateMinutes])
  const session = useSession({
    busy: conversation.busy,
    conversation,
    showToast: toast.show,
    onEnterChat: () => setView('chat'),
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
            setDrafts((d) => {
              const next = { ...d }
              delete next[id]
              return next
            })
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
                recorderCard={<LiveRecorderCard rec={recorder} onOpenWindow={() => void showRecorderWindow()} />}
                onGenerateMinutes={(m) => void generateMinutes(m)}
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
                onSend={(text, attach, display) => void conversation.send(text, attach, display)}
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
    </div>
  )
}
