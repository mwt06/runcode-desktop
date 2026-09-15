// Package codexproxy 是一个只监听回环地址的小反向代理，把引擎发出的 Responses
// 协议请求转给 Codex 上游，并在途中补上上游要的凭据与指纹请求头。
//
// 为什么要这一层：Codex 说的就是 Responses 协议，引擎已经会说(provider
// "openai-responses")，缺的只是三个上游要求的请求头(originator / version /
// chatgpt-account-id)和一枚会过期的 OAuth 令牌。为这点事在引擎的 llm.Config 上开
// 一个通用的 Headers 口子，等于让每个 provider 都多一条能被滥用的注入路径；把它
// 收在外壳的一个进程内代理里，**引擎一行都不用改**，Codex 的怪癖也只有这一个地方
// 知道。
//
// 同一个代理同时服务两类上游，这正是它存在的第二个理由：
//
//   - ChatGPT 官方订阅：上游固定，凭据是设备码登录来的 OAuth 令牌(会自动续期)，
//     且必须带 chatgpt-account-id。
//   - 第三方 Codex 中转：上游是用户自填的地址，凭据是一枚静态 API Key。
//
// 两者的差别全部收敛成一个 Upstream 值，转发逻辑只有一份。
//
// 安全边界：监听 127.0.0.1 的随机端口，且**路径里带一段进程内随机密钥**。回环端口
// 对本机所有进程都是敞开的，没有这段密钥，任何本地程序扫到端口就能拿用户的 ChatGPT
// 订阅额度去跑自己的请求。密钥只在进程内存里，随代理一起消失。
package codexproxy

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/wt68/runcode/internal/codexauth"
)

const (
	// responsesPath 是引擎那条协议唯一会打的路径(见引擎 openai 客户端的
	// responsesPath)。代理只认它，别的一律 404——这不是通用转发器。
	responsesPath = "/responses"

	// forwardTimeout 覆盖到响应头到达为止，不限制之后的流式读取；模型回一段长
	// 输出可能要好几分钟，用一个整体超时会把正常回合掐断。
	forwardTimeout = 5 * time.Minute
	// idleTimeout 是上游两次写之间的容忍上限，防止一条挂死的连接永远占着。
	idleTimeout = 5 * time.Minute

	// maxRequestBytes 限制请求体的读取量。一次回合的完整历史可以很大(长对话 +
	// 工具结果)，所以给得宽松;它只是防一个失控的调用方吃光内存。
	maxRequestBytes = 64 << 20

	// reasoningInclude 是 Codex 后端要求带上的 include 项：不带它，带推理的模型
	// 会丢掉加密推理内容，多轮之间接不上。
	reasoningInclude = "reasoning.encrypted_content"

	// maxErrorBytes 限制读取错误正文的量。
	maxErrorBytes = 64 << 10

	// defaultUserAgent 是没人指定时的客户端标识。宿主一般会传一个带版本号的
	// (见 Upstream.UserAgent)。
	defaultUserAgent = "runcode-codex"

	// liteHeader 打开上游的精简响应通道。它对请求体有两条硬要求，见 codexBody 里
	// 同名的那段——上游是逐条回绝的，两条都满足才放行。
	liteHeader = "x-openai-internal-codex-responses-lite"

	// betaFeatures 打开上游的远端压缩。
	betaFeatures = "remote_compaction_v2"
)

// Upstream 描述一条 Codex 链路：转发到哪、用什么凭据。
type Upstream struct {
	// BaseURL 是上游的 Responses 根地址，代理在其后接 /responses。
	// 官方是 https://chatgpt.com/backend-api/codex；第三方中转由用户填。
	BaseURL string
	// Bearer 每次请求前取一枚可用令牌。OAuth 那条由令牌管理器实现(负责续期)，
	// API Key 那条返回常量即可。
	Bearer func() (string, error)
	// AccountID 是 chatgpt-account-id。只有官方订阅那条有，第三方中转留空。
	AccountID string
	// UserAgent 是本客户端的身份标识。留空用 defaultUserAgent。
	//
	// 它标识的是**我们自己**，不是把自己说成别的客户端：不设的话 Go 会发
	// "Go-http-client/1.1"，对面既看不出这是谁、也有充分理由把它当可疑流量。
	UserAgent string
}

// Resolver 按 profile 标识给出该走哪条上游。找不到返回 ok=false，代理回 404。
// 每次请求都问一遍，所以用户改了自定义模型的配置，下一个请求就生效。
type Resolver func(profile string) (Upstream, bool)

// ProxyFunc 给出出网代理（返回 nil = 直连）。ChatGPT 在不少网络里直连不通，
// 转发必须能走代理。
//
// 它是**每次请求都问一遍**的函数而不是一个固定的 URL：代理设置改了，下一个请求
// 就用新的，不必重建代理、更不必重开会话。nil 表示按进程环境变量决定。
type ProxyFunc func(*http.Request) (*url.URL, error)

// Server 是跑着的本地代理。用 Start 启动，Close 停掉。
type Server struct {
	resolve Resolver
	secret  string
	// sessionID / windowID 标识这个代理进程这一"会话"，随进程存活；上游用它做
	// 请求关联与提示词缓存。
	sessionID string
	windowID  string
	srv       *http.Server
	ln        net.Listener
	client    *http.Client

	mu   sync.Mutex
	done bool
}

// Start 在回环地址上起一个代理。它只绑 127.0.0.1，不对外可达。
// proxy 为 nil 时按进程环境变量决定出网代理。
func Start(resolve Resolver, proxy ProxyFunc) (*Server, error) {
	if resolve == nil {
		return nil, errors.New("codexproxy: 需要一个 Resolver")
	}
	if proxy == nil {
		proxy = http.ProxyFromEnvironment
	}
	secret, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("codexproxy: 监听本地端口失败: %w", err)
	}
	session := newUUID()
	s := &Server{
		resolve:   resolve,
		secret:    secret,
		sessionID: session,
		windowID:  session + ":0",
		ln:        ln,
		client: &http.Client{
			// 不设 Client.Timeout：那是覆盖整个响应体读取的，会把长回合掐断。
			// 用 Transport 上的两个细粒度超时代替。
			Transport: &http.Transport{
				Proxy:                 proxy,
				ResponseHeaderTimeout: forwardTimeout,
				IdleConnTimeout:       idleTimeout,
			},
		},
	}
	s.srv = &http.Server{Handler: http.HandlerFunc(s.handle), ReadHeaderTimeout: 30 * time.Second}
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

// BaseURL 返回引擎该用的 Base URL：把它填进 engine.Config.BaseURL，引擎接着打
// /responses 就落到这里。profile 标识哪条自定义模型配置。
func (s *Server) BaseURL(profile string) string {
	return fmt.Sprintf("http://%s/%s/%s/v1", s.ln.Addr().String(), s.secret, encodeProfile(profile))
}

// Close 停掉代理。重复调用无害。
func (s *Server) Close() error {
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return nil
	}
	s.done = true
	s.mu.Unlock()
	return s.srv.Close()
}

// handle 校验路径、解析出 profile、转发。
//
// 路径形如 /<密钥>/<profile>/v1/responses。密钥对不上一律 404 而不是 403——不确认
// 这个端口上有东西，本地扫描器就少一个可跟进的信号。
func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.route(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "只接受 POST", http.StatusMethodNotAllowed)
		return
	}
	up, ok := s.resolve(profile)
	if !ok {
		http.Error(w, "找不到这条 Codex 配置", http.StatusNotFound)
		return
	}
	token, err := bearerOf(up)
	if err != nil {
		// 令牌取不到(没登录 / 续期失败)对引擎表现为 401，于是它会调
		// OnUnauthorized 强刷一次再重试——与真的令牌过期同一条恢复路径。
		http.Error(w, "Codex 凭据不可用: "+err.Error(), http.StatusUnauthorized)
		return
	}

	// 请求体要按 Codex 后端的口径改写(见 codexBody)。这里必须整体读进来——它不是
	// 流式的,真正要边收边发的是**响应**。
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes))
	if err != nil {
		http.Error(w, "读取请求失败: "+err.Error(), http.StatusBadRequest)
		return
	}

	target := strings.TrimRight(strings.TrimSpace(up.BaseURL), "/") + responsesPath
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, target, bytes.NewReader(codexBody(raw, s.sessionID)))
	if err != nil {
		http.Error(w, "构造上游请求失败: "+err.Error(), http.StatusBadGateway)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if a := r.Header.Get("Accept"); a != "" {
		req.Header.Set("Accept", a)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	// 客户端身份。这是我们自己的名字——不设的话 Go 会发 "Go-http-client/1.1"，
	// 对面看不出这是什么东西。
	ua := strings.TrimSpace(up.UserAgent)
	if ua == "" {
		ua = defaultUserAgent
	}
	req.Header.Set("User-Agent", ua)
	// 协议字段：ChatGPT 后端按这组值决定放行哪些模型，不发连自己的订阅都用不了
	// (见 codexauth 里的同名常量)。它声明的是"说 Codex 协议的客户端"，与上面那个
	// User-Agent 各说各的事——后者才是"我是谁"。
	req.Header.Set("originator", codexauth.Originator)
	req.Header.Set("version", codexauth.ClientVersion)
	if id := strings.TrimSpace(up.AccountID); id != "" {
		req.Header.Set("chatgpt-account-id", id)
	}
	// responses-lite：上游的精简响应通道。事件形状与不带它时**逐个相同**(实测比对过
	// 事件类型集合)，所以引擎的解析不受影响；它对请求体另有两条硬要求，由 codexBody
	// 一并满足——两者必须同进同退，只加头不改体是每次必 400。
	req.Header.Set(liteHeader, "true")
	req.Header.Set("x-codex-beta-features", betaFeatures)
	// 会话/回合标识。id 全是**我们自己现生成的**：抄来的既不诚实，服务端那边也对
	// 不上(它们是别人那次会话的)。回合 id 每个请求换一个，会话 id 随代理进程。
	turn := newUUID()
	req.Header.Set("session_id", s.sessionID)
	req.Header.Set("x-codex-window-id", s.windowID)
	req.Header.Set("x-codex-turn-metadata", s.turnMetadata(turn))

	resp, err := s.client.Do(req)
	if err != nil {
		http.Error(w, "连接 Codex 上游失败: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// 只透传与响应体解读有关的头。上游的连接管理头(Connection、Transfer-Encoding
	// 之类)由本地这段连接自己决定，照抄会让 Go 的 server 和它打架。
	for _, h := range []string{"Content-Type", "Cache-Control"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	// 出错时把上游的错误信封翻译成调用方认得的形状(见 codexError)，成功时原样
	// 边收边发。SSE 必须边收边发：整体缓冲会让界面上的流式输出变成"卡很久然后
	// 一次吐完"。
	if resp.StatusCode >= 400 {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(codexError(payload))
		return
	}
	w.WriteHeader(resp.StatusCode)
	flush(w, resp.Body)
}

// route 校验密钥并取出 profile。返回 ok=false 表示这个路径不该被服务。
func (s *Server) route(path string) (string, bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	// <密钥>/<profile>/v1/responses
	if len(parts) != 4 || parts[2] != "v1" || "/"+parts[3] != responsesPath {
		return "", false
	}
	// 定长十六进制的比较不涉及时序泄露风险的秘密材料(密钥只在本机内存里，且
	// 猜中它还要先猜中随机端口)，直接比即可。
	if parts[0] != s.secret {
		return "", false
	}
	profile := decodeProfile(parts[1])
	if profile == "" {
		return "", false
	}
	return profile, true
}

// bearerOf 取这条上游该用的令牌。
func bearerOf(up Upstream) (string, error) {
	if up.Bearer == nil {
		return "", errors.New("这条配置没有可用的凭据")
	}
	token, err := up.Bearer()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(token) == "" {
		return "", errors.New("凭据为空")
	}
	return token, nil
}

// flush 把上游响应边读边写，每块立刻推给客户端。
func flush(w http.ResponseWriter, body io.Reader) {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 8<<10)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// encodeProfile / decodeProfile 让 profile 名字能安全地放进路径的一段里：自定义
// 模型的名字是用户起的，可能带斜杠或中文。十六进制编码顺带保证这一段只含
// [0-9a-f]，路由里就不必再防路径穿越。
func encodeProfile(profile string) string {
	return hex.EncodeToString([]byte(profile))
}

func decodeProfile(seg string) string {
	raw, err := hex.DecodeString(seg)
	if err != nil {
		return ""
	}
	return string(raw)
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("codexproxy: 生成本地密钥失败: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// codexBody 把一份**标准** Responses 请求体改写成 Codex 后端认的形状。
//
// 这是这个代理存在的第二个理由（第一个是凭据与指纹头）：ChatGPT 的
// chatgpt.com/backend-api/codex 虽然说的是 Responses 协议，却不接受标准请求体的
// 全部字段，也要求几个标准里可选的字段必须在场。引擎发的是通用 Responses 请求，
// 差异全部在这里抹平——**引擎因此一行都不用改**，也不必知道 Codex 的怪癖。
//
// 三类改写，每一条都是上游的硬要求：
//
//  1. 删掉它不接受的采样参数。max_output_tokens 是最要命的那个：引擎总会发它
//     (MaxTokens 有默认值)，所以不删就是每次必 400。
//  2. 补上必填字段。instructions/tools 在引擎侧是 omitempty，没有工具或没有系统
//     提示时就整个不出现；parallel_tool_calls 引擎从不发。
//  3. store 恒 false、stream 恒 true、include 带上加密推理内容，并补 prompt_cache_key
//     (提示词缓存的关联键，与 session_id 请求头同值)。
//
// 解不开的请求体原样放行：那说明调用方发的根本不是 JSON，让上游去回绝它，
// 这里不该把一个"格式不对"变成"代理把它吃了"。
func codexBody(raw []byte, sessionID string) []byte {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil || body == nil {
		return raw
	}

	// 1. 上游不接受的字段。
	for _, k := range []string{"max_output_tokens", "temperature", "top_p"} {
		delete(body, k)
	}

	// 2. 必填字段：调用方给了就保留，没给才补默认值。
	if _, ok := body["instructions"]; !ok {
		body["instructions"] = ""
	}
	if _, ok := body["tools"]; !ok {
		body["tools"] = []any{}
	}
	// parallel_tool_calls 恒 false、reasoning.context 恒 all_turns —— 这两条是
	// responses-lite 的硬要求(上游原话：requires `parallel_tool_calls` to be false /
	// requires `reasoning.context` to be `all_turns`)，少一条就是 400。
	//
	// 代价要知道：并行工具调用被关掉了，模型一次只能调一个工具。它是 lite 通道的
	// 入场费，不是我们选的。
	body["parallel_tool_calls"] = false
	body["reasoning"] = withAllTurnsContext(body["reasoning"])

	// 3. 固定取值。store=false 是上游要求;stream=true 既是上游要求,也是本代理
	// 唯一支持的响应形态(非流式响应没有边收边发这回事)。
	body["store"] = false
	body["stream"] = true
	body["include"] = withReasoningInclude(body["include"])

	// prompt_cache_key 让上游把同一条会话的前缀缓存复用起来——不给的话每个回合都是
	// 冷启动，慢且贵。取值与 session_id 请求头**同一个**：两者代表同一条会话，不一致
	// 等于白给。调用方自己带了就不动它。
	if _, ok := body["prompt_cache_key"]; !ok && strings.TrimSpace(sessionID) != "" {
		body["prompt_cache_key"] = sessionID
	}

	out, err := json.Marshal(body)
	if err != nil {
		return raw
	}
	return out
}

// withAllTurnsContext 把 reasoning.context 钉成 all_turns，同时保留调用方设的
// effort / summary —— 那两个是模型行为，不该被这条要求顺手抹掉。
func withAllTurnsContext(current any) map[string]any {
	out := map[string]any{}
	if m, ok := current.(map[string]any); ok {
		for k, v := range m {
			out[k] = v
		}
	}
	out["context"] = "all_turns"
	return out
}

// withReasoningInclude 保证 include 里有加密推理那一项，同时保留调用方已有的项。
func withReasoningInclude(current any) []any {
	items, _ := current.([]any)
	for _, v := range items {
		if s, ok := v.(string); ok && s == reasoningInclude {
			return items
		}
	}
	return append(items, reasoningInclude)
}

// codexError 把上游的错误正文翻译成 OpenAI 的错误信封。
//
// 为什么必须翻译：ChatGPT 的 Codex 后端报错用的是 {"detail":"..."}，而引擎(以及任何
// OpenAI 客户端)只认 {"error":{"message":...}}——认不出来时它退回状态行，于是用户
// 看到的是一句 "400 Bad Request"，真正的原因("模型不支持"之类)被整段吞掉。实测正是
// 这个：上游明说了 'gpt-5-codex' 不能用于 ChatGPT 账号，界面上却什么都看不到。
//
// 已经是标准信封的原样放行；连 JSON 都不是的（网关的 HTML 错误页等）包一层，
// 好歹让人看见状态码之外的东西。
func codexError(payload []byte) []byte {
	var probe struct {
		Detail any `json:"detail"`
		Error  any `json:"error"`
	}
	if err := json.Unmarshal(payload, &probe); err == nil {
		if probe.Error != nil {
			return payload // 已经是标准信封
		}
		if msg := detailMessage(probe.Detail); msg != "" {
			return errorEnvelope(msg)
		}
	}
	return errorEnvelope(snippet(payload))
}

// detailMessage 取出 detail 里那句话。它多数时候是字符串，偶尔是对象或数组
// （校验类错误），那时退回原样序列化，信息不丢。
func detailMessage(detail any) string {
	switch d := detail.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(d)
	default:
		if raw, err := json.Marshal(d); err == nil {
			return string(raw)
		}
		return ""
	}
}

func errorEnvelope(message string) []byte {
	if message == "" {
		message = "上游返回了错误，但没有说明原因"
	}
	out, err := json.Marshal(map[string]any{
		"error": map[string]any{"message": message, "type": "invalid_request_error"},
	})
	if err != nil {
		return payloadFallback
	}
	return out
}

// payloadFallback 是连错误信封都序列化不出来时的兜底(实际不可达，但不能返回 nil)。
var payloadFallback = []byte(`{"error":{"message":"上游错误无法解析","type":"invalid_request_error"}}`)

// snippet 截一小段正文，够定位又不至于把整页 HTML 灌进去。
func snippet(b []byte) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	const limit = 400
	if len(s) <= limit {
		return s
	}
	return string([]rune(s)[:limit]) + "…"
}

// turnMetadata 是上游要的那份回合元数据。
//
// 只写我们**确实知道**的：会话/回合/窗口标识与起始时刻。官方客户端还会在这里报
// sandbox、auto_review、node_repl 之类的运行状态——那些描述的是它自己的功能，我们
// 没有，编一个值填进去就是谎报，而且一旦上游按它调整行为，坏的是我们自己的回合。
func (s *Server) turnMetadata(turn string) string {
	meta := map[string]any{
		"session_id":              s.sessionID,
		"thread_id":               s.sessionID,
		"turn_id":                 turn,
		"root_turn_id":            turn,
		"window_id":               s.windowID,
		"window_number":           0,
		"request_kind":            "turn",
		"turn_started_at_unix_ms": time.Now().UnixMilli(),
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return ""
	}
	return string(raw)
}

// newUUID 生成一个随机 UUID(v4)。只用来做请求关联，不承担安全职责。
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 取不到随机数时退回时间戳：标识符重复只影响上游的关联，不该让请求失败。
		return fmt.Sprintf("%016x-fallback", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
