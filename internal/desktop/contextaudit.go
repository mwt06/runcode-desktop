package desktop

// 上下文审核（仅测试版构建，见 testbuild.go）：开启后，引擎每次发给模型的完整
// 请求上下文——系统提示词、全部消息历史、工具清单——按会话落成 JSONL，并由
// contextaudit_server.go 的本地页面提供查看。数据链路：引擎的
// Options.LLMRequestObserver（所有用途的请求都过同一个观测点）→ observer 闭包
// （原子开关，关着时零开销）→ contextAuditStore 追加落盘。
//
// 记录是"发出去什么就存什么"的忠实快照，唯一的例外是图片：base64 原始字节被
// 脱水成尺寸占位，否则一次带图回合就能把审核文件撑到几十 MB。同理不存工具的
// InputSchema——它逐请求不变，名字与描述已足够核对提示词。

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
)

// auditRecord 是一条审核记录：一次实际发往模型的完整请求。
type auditRecord struct {
	Time      time.Time      `json:"time"`
	SessionID string         `json:"sessionId"`
	TurnID    string         `json:"turnId"`
	Purpose   string         `json:"purpose"`
	Model     string         `json:"model"`
	MaxTokens int            `json:"maxTokens,omitempty"`
	Thinking  string         `json:"thinking,omitempty"`
	Breakdown auditBreakdown `json:"breakdown"`
	System    []auditBlock   `json:"system,omitempty"`
	Messages  []auditMessage `json:"messages,omitempty"`
	Tools     []auditTool    `json:"tools,omitempty"`
}

// auditBreakdown 是一条记录的体积构成。
//
// 诊断上下文膨胀时第一个要问的不是"总共多少 token"，而是"这些 token 是谁占的" ——
// 是系统提示词、工具清单，还是某几条工具结果。没有这份拆分，一份 20MB 的落盘只能
// 靠临时脚本去分析；有了它，同样的结论一眼可见。
//
// token 数走 llm.EstimateRequestTokens 同一套启发式，与引擎里真正触发压缩的判据
// 完全一致 —— 另写一份近似会让这里显示的数字和实际行为脱节，那比不显示更糟。
type auditBreakdown struct {
	// EstTokens 是整条请求的估算 token 数（含系统提示词与工具 schema）。
	EstTokens int `json:"estTokens"`
	// 按来源拆分。SystemTokens 与 ToolsTokens 是每轮固定开销，压缩动不了它们；
	// 其余四项来自对话历史，是压缩与脱水能影响的部分。
	SystemTokens     int `json:"systemTokens"`
	ToolsTokens      int `json:"toolsTokens"`
	ToolResultTokens int `json:"toolResultTokens"`
	ToolUseTokens    int `json:"toolUseTokens"`
	ThinkingTokens   int `json:"thinkingTokens"`
	TextTokens       int `json:"textTokens"`
	Messages         int `json:"messages"`
	// UserMessages 是真实对话轮数。它与 Messages 的比例是"回合内膨胀"的直接信号：
	// 两三条用户消息配上一百多条消息，说明单个回合跑了几十轮工具。
	UserMessages int `json:"userMessages"`
	// Largest 是最占体积的若干条负载，直接指向该处理哪里。
	Largest []auditLargest `json:"largest,omitempty"`
}

// auditLargest 定位一条大负载。
type auditLargest struct {
	Kind         string `json:"kind"`
	Tool         string `json:"tool,omitempty"`
	MessageIndex int    `json:"messageIndex"`
	EstTokens    int    `json:"estTokens"`
	// Age 是这条负载之后还有多少条消息。年龄越大越该被脱水，所以它是判断
	// "该不该被处理掉却还留着"的关键一列。
	Age int `json:"age"`
}

type auditMessage struct {
	Role    string       `json:"role"`
	Content []auditBlock `json:"content,omitempty"`
}

// auditBlock 对应 llm.ContentBlock，图片内容脱水成占位文本。
type auditBlock struct {
	Type      string       `json:"type"`
	Text      string       `json:"text,omitempty"`
	Name      string       `json:"name,omitempty"`
	ID        string       `json:"id,omitempty"`
	ToolUseID string       `json:"toolUseId,omitempty"`
	Input     string       `json:"input,omitempty"`
	IsError   bool         `json:"isError,omitempty"`
	Cache     string       `json:"cache,omitempty"`
	Content   []auditBlock `json:"content,omitempty"`
}

type auditTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// auditSessionSummary 是查看页会话列表的一行。
type auditSessionSummary struct {
	ID       string    `json:"id"`
	Requests int       `json:"requests"`
	Bytes    int64     `json:"bytes"`
	LastTime time.Time `json:"lastTime"`
	Model    string    `json:"model,omitempty"`
}

// buildAuditRecord snapshots one outbound request. It runs inside the engine's
// observer callback, before the request is sent: every kept string is copied by
// value here (Go strings are immutable), so nothing aliases the live request
// after this returns.
func buildAuditRecord(sessionID, purpose, turnID string, req llm.Request) auditRecord {
	rec := auditRecord{
		Time:      time.Now(),
		SessionID: sessionID,
		TurnID:    turnID,
		Purpose:   purpose,
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		Thinking:  string(req.Thinking.Effort),
		Breakdown: buildAuditBreakdown(req),
		System:    auditBlocks(req.System),
	}
	rec.Messages = make([]auditMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		rec.Messages = append(rec.Messages, auditMessage{Role: string(m.Role), Content: auditBlocks(m.Content)})
	}
	if len(req.Tools) > 0 {
		rec.Tools = make([]auditTool, 0, len(req.Tools))
		for _, t := range req.Tools {
			rec.Tools = append(rec.Tools, auditTool{Name: t.Name, Description: t.Description})
		}
	}
	return rec
}

func auditBlocks(blocks []llm.ContentBlock) []auditBlock {
	if len(blocks) == 0 {
		return nil
	}
	out := make([]auditBlock, 0, len(blocks))
	for _, b := range blocks {
		ab := auditBlock{
			Type:      string(b.Type),
			Text:      b.Text,
			Name:      b.Name,
			ID:        b.ID,
			ToolUseID: b.ToolUseID,
			Input:     string(b.Input),
			IsError:   b.IsError,
			Cache:     string(b.Cache),
			Content:   auditBlocks(b.Content),
		}
		// 图片脱水:原始字节(base64 后更大)不进审核文件,留下可核对的占位。
		if b.Type == llm.ContentBlockTypeImage && b.Source != nil {
			ab.Text = fmt.Sprintf("[图片 %s，%d 字节]", b.Source.MediaType, len(b.Source.Data))
			if b.Source.URL != "" {
				ab.Text = fmt.Sprintf("[图片 %s]", b.Source.URL)
			}
		}
		out = append(out, ab)
	}
	return out
}

// contextAuditDir 是审核记录的落盘目录（每会话一个 JSONL）。纯路径计算，不建目录。
func contextAuditDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "runcode", "context-audit"), nil
}

// contextAuditStore 追加式落盘：<dir>/<sessionID>.jsonl，每行一条 auditRecord。
// mu 串行化并发追加（回合请求与自动标题可能同时在飞）。
type contextAuditStore struct {
	mu  sync.Mutex
	dir string
}

func newContextAuditStore() (*contextAuditStore, error) {
	dir, err := contextAuditDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &contextAuditStore{dir: dir}, nil
}

// sanitizeAuditID maps a session id to a safe file stem; anything outside
// [A-Za-z0-9._-] becomes '_' so an id can never traverse out of the audit dir.
func sanitizeAuditID(id string) string {
	if id == "" {
		return "_"
	}
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	s := strings.Trim(b.String(), ".")
	if s == "" {
		return "_"
	}
	return s
}

func (s *contextAuditStore) append(rec auditRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, sanitizeAuditID(rec.SessionID)+".jsonl")
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // G304: path 由 sanitizeAuditID 约束在审核目录内
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// sessionSummaries 列出目录下的会话及其概况（供查看页侧栏）。逐文件扫描：这是
// 本地测试工具，文件量小，实时扫描换来的是无需维护索引。
func (s *contextAuditStore) sessionSummaries() ([]auditSessionSummary, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	out := make([]auditSessionSummary, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		sum := auditSessionSummary{ID: strings.TrimSuffix(name, ".jsonl"), Bytes: info.Size(), LastTime: info.ModTime()}
		lines, err := s.readLines(name)
		if err == nil {
			sum.Requests = len(lines)
			// 末条记录的模型代表会话当前所用模型。
			for i := len(lines) - 1; i >= 0; i-- {
				var meta struct {
					Model string `json:"model"`
				}
				if json.Unmarshal(lines[i], &meta) == nil && meta.Model != "" {
					sum.Model = meta.Model
					break
				}
			}
		}
		out = append(out, sum)
	}
	return out, nil
}

// readSession 返回一个会话的全部记录（原始 JSON 行）。id 先过 sanitize，改写即拒绝，
// 保证只读审核目录内的文件。
func (s *contextAuditStore) readSession(id string) ([]json.RawMessage, error) {
	if id == "" || sanitizeAuditID(id) != id {
		return nil, errors.New("无效的会话 id")
	}
	return s.readLines(id + ".jsonl")
}

func (s *contextAuditStore) readLines(name string) ([]json.RawMessage, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, name)) //nolint:gosec // G304: name 经 sanitize/固定后缀,限定在审核目录内
	if err != nil {
		return nil, err
	}
	var out []json.RawMessage
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, json.RawMessage(line))
	}
	return out, nil
}

// contextAuditManager 聚合运行态：开关（原子读，观测器热路径不加锁）、存储与查看
// 服务器的生命周期。持久化（desktop.json 的 ContextAudit 字段）在 App 命令层。
type contextAuditManager struct {
	mu      sync.Mutex
	enabled atomic.Bool
	store   *contextAuditStore
	srv     *contextAuditServer
	url     string
}

func newContextAuditManager() *contextAuditManager { return &contextAuditManager{} }

// enable 建目录、起查看服务器并打开开关；已开启时幂等返回现状。
func (m *contextAuditManager) enable() (ContextAuditInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store == nil {
		store, err := newContextAuditStore()
		if err != nil {
			return ContextAuditInfo{}, err
		}
		m.store = store
	}
	if m.srv == nil {
		srv := newContextAuditServer()
		url, err := srv.start(m.store)
		if err != nil {
			return ContextAuditInfo{}, err
		}
		m.srv, m.url = srv, url
	}
	m.enabled.Store(true)
	return m.statusHeld(), nil
}

// disable 关闭开关并停掉查看服务器；落盘的记录保留。
func (m *contextAuditManager) disable() ContextAuditInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled.Store(false)
	if m.srv != nil {
		m.srv.stop()
		m.srv, m.url = nil, ""
	}
	return m.statusHeld()
}

func (m *contextAuditManager) status() ContextAuditInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusHeld()
}

func (m *contextAuditManager) statusHeld() ContextAuditInfo {
	info := ContextAuditInfo{Supported: IsTestBuild(), Enabled: m.enabled.Load(), URL: m.url}
	if m.store != nil {
		info.Dir = m.store.dir
	} else if dir, err := contextAuditDir(); err == nil {
		info.Dir = dir
	}
	return info
}

// observer 返回接给引擎 Options.LLMRequestObserver 的回调。开关关闭时仅一次原子
// 读即返回；记录失败只进诊断日志，绝不影响回合。
func (m *contextAuditManager) observer(sessionID string) func(purpose, turnID string, req llm.Request) {
	return func(purpose, turnID string, req llm.Request) {
		if !m.enabled.Load() {
			return
		}
		m.mu.Lock()
		store := m.store
		m.mu.Unlock()
		if store == nil {
			return
		}
		if err := store.append(buildAuditRecord(sessionID, purpose, turnID, req)); err != nil {
			debugLog("context audit append: %v", err)
		}
	}
}

// ContextAuditStatus 报告上下文审核的当前状态（是否测试版、开关、查看地址、目录）。
func (a *App) ContextAuditStatus() (ContextAuditInfo, error) {
	return a.audit.status(), nil
}

// SetContextAudit 开关上下文审核并持久化。仅测试版构建允许开启；关闭总是允许
// （正式版里它本来就不可能开着）。运行态先行、持久化随后：持久化失败会回滚刚
// 打开的运行态，保证界面所见与重启后的行为一致。
func (a *App) SetContextAudit(enabled bool) (ContextAuditInfo, error) {
	if enabled && !IsTestBuild() {
		return ContextAuditInfo{}, wireError(errors.New("上下文审核仅测试版构建可用"))
	}
	var info ContextAuditInfo
	if enabled {
		var err error
		info, err = a.audit.enable()
		if err != nil {
			return ContextAuditInfo{}, wireError(err)
		}
	} else {
		info = a.audit.disable()
	}
	if err := updateRawConfig(func(cfg *StartSessionRequest) error {
		cfg.ContextAudit = enabled
		return nil
	}); err != nil {
		if enabled {
			a.audit.disable()
		}
		return ContextAuditInfo{}, wireError(err)
	}
	return info, nil
}
