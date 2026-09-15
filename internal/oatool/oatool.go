// Package oatool implements the desktop's OA office tools: 15 read-only
// capabilities of the school's OA system (待办/已办/流程详情、通讯录、公文检索与
// 正文), reached through the platform Bridge with the signed-in user's Passport
// token.
//
// # 身份
//
// 工具的入参里没有任何身份字段(见 catalog.go 的 schema 注释)。调用者是谁完全由
// Authorization 头里的令牌决定,服务端验签后换成 OA 的人员 id 再去查——所以每个人
// 只能读到自己在 OA 里的数据,模型既改不了也看不见这件事。
//
// # 本地模型闸(Gate)
//
// 这是本包存在的第二个理由,也是它与 websearchtool 唯一的结构性差别。OA 数据是
// 保密数据,只允许进内网部署的模型。Gate 在**发出任何 OA 请求之前**被调用:它说
// 不行,本次调用就到此为止,一个字节都不会从 OA 取出来。
//
// 注意这个顺序不能反过来。"先取回来、发现模型不对再丢掉"是没有意义的——数据一旦
// 进了工具结果就会进会话历史,而历史是要发给模型的。安全性来自"没产生",不是
// "产生了但没用"。
//
// # 为什么是内置工具而不是 MCP
//
// MCP 那条路(oa-mcp-server + config.toml)绕不开 Gate:MCP 工具由引擎统一装配,
// 宿主没有地方插入"这次不许调"的判断。内置工具则由宿主自己造,闸门就在构造它的
// 那只手里。
package oatool

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

const (
	// defaultTimeout 比服务端宽:OA 自身单次调用 25s,而 oa_doc_content 遇到扫描件
	// 公文还要再跑一轮视觉识别。客户端先超时的话,用户看到的是"工具失败",服务端
	// 那边的请求却还在跑——排查时最误导人的一种表现。
	defaultTimeout = 120 * time.Second
	// maxBodyBytes 要容得下扫描件公文的原图(base64 后还要涨三分之一)。服务端已经
	// 按页数与单页字节各卡了一道,这里是最后一道,防的是一个坏掉/被篡改的响应。
	maxBodyBytes = 32 << 20
	// maxImages/maxImageBytes 是客户端这一侧的上限。服务端也有一份(MAX_DOC_IMAGES /
	// MAX_DOC_IMAGE_BYTES),两边都卡不是重复:服务端那份是**上下文预算**(内网模型
	// 窗口约 32k,一页扫描件要 700～2700 个视觉 token),这一份是**不信任上游**——
	// 客户端不该因为服务端某天改了配置就把几十兆塞进会话历史。
	maxImages     = 8
	maxImageBytes = 8 << 20
	// maxErrorText 截断上游错误正文,避免把一整段异常栈塞进上下文。
	maxErrorText = 512
)

// Config 是这批工具的接线。Endpoint 与 Token 必填。
type Config struct {
	// Endpoint 是 OA 接口的基地址(形如 https://bridge/t/<租户>/v1/oa),
	// 具体动作拼在后面。租户已经在基地址里,不必另外传。
	Endpoint string
	// Token 每次调用前取一枚可用的访问令牌(与引擎的 TokenSource 同一个函数),
	// 由它负责临期续期与并发去重。
	Token func() (string, error)
	// OnUnauthorized 在服务端回 401 时调用一次以强制刷新令牌,随后本次调用重试
	// 一次。nil 表示不重试。与 LLM 请求、联网搜索同一套语义。
	OnUnauthorized func()
	// Gate 在每次工具执行前求值,返回非 nil 则**不发出任何 OA 请求**,其文本作为
	// 工具结果(IsError)回给模型。宿主用它实现"OA 数据只进本地模型"。
	// nil = 不设闸(仅供测试与将来可能的非保密场景)。
	//
	// toolUseID 是这一次调用的 id(来自 tool.Context),让宿主能把"这张卡片是被
	// 策略拦下的,不是真失败"精确告诉界面——否则界面只能按文本猜,而按文案匹配的
	// 东西会在下一次改措辞时静默失效。
	Gate func(ctx context.Context, toolName, toolUseID string) error
	// Client 是发请求用的 HTTP 客户端。nil 用一个带超时的普通客户端——**不能**用
	// 引擎那个加固客户端:它拒连回环/内网地址,而 Bridge 与 OA 都在内网。
	// 与 websearchtool 同一条理由。
	Client *http.Client
}

// New 按 cfg 造出全部 OA 工具。Endpoint 或 Token 缺一则返回 nil,宿主据此干脆
// 不注册——装一批注定每次都失败的工具比没有更糟,而且白占上下文。
func New(cfg Config) []tool.Tool {
	if strings.TrimSpace(cfg.Endpoint) == "" || cfg.Token == nil {
		return nil
	}
	cfg.Endpoint = strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: defaultTimeout}
	}
	out := make([]tool.Tool, 0, len(catalog))
	for _, d := range catalog {
		out = append(out, Tool{cfg: cfg, desc: d})
	}
	return out
}

// Tool 是一条 OA 只读工具。
type Tool struct {
	cfg  Config
	desc descriptor
}

// Name 是模型看到的工具名(带 oa_ 前缀,见 catalog.go)。它同时是权限归类与关闭
// 清单的寻址键,改名等于改这三处。
func (t Tool) Name() string { return t.desc.name }

// Description 是这条能力给模型的说明,逐字沿用服务端调过的那份文案。
func (t Tool) Description() string { return t.desc.desc }

// InputSchema 由 descriptor 的参数表拼出,其中**没有任何身份字段**。
func (t Tool) InputSchema() tool.Schema { return t.desc.schema() }

// IsConcurrencySafe 报 true:一次调用只读远端,不碰任何共享状态。
//
// 并发压力靠服务端限流兜,不靠这里串行化:限流只有服务端做得对(它看得见所有客户端,
// 而 OA 是学校的生产系统),在客户端串行化既拦不住多开的客户端,又白白让"列个待办
// 再看三条详情"这种回合慢上几倍。
func (Tool) IsConcurrencySafe() bool { return true }

// invokeRequest 是 POST {endpoint}/invoke 的请求体。
type invokeRequest struct {
	Tool string            `json:"tool"`
	Args map[string]string `json:"args,omitempty"`
}

// invokeEnvelope 是服务端的统一返回信封。
//
// State=false 是**业务失败**,不是传输失败:没绑 OA、身份服务抖动、OA 自己拒绝,
// 三者的应对完全不同,所以 Code 必须分开而不是塌成一句"查询失败"。
type invokeEnvelope struct {
	State   bool            `json:"state"`
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
	// Images 是随结果附上的原图(目前只有扫描件公文会有)。它与 Data 平级而不是塞在
	// Data 里:Data 的语义是"给模型读的那份 JSON",原样当文本透出;图片要变成工具
	// 结果里的 image 块,是另一种东西。
	Images []envelopeImage `json:"images"`
}

// envelopeImage 是一张随结果附上的原图。Data 是 base64。
type envelopeImage struct {
	MediaType string `json:"mediaType"`
	Data      string `json:"data"`
}

// 服务端的失败分类。客户端不翻译这些码面向用户的文案(Message 由服务端给,那边
// 才知道究竟是哪一环断的),只用它决定"这次失败值不值得重试"这类客户端判断。
const (
	CodeNoOAAccess          = "no_oa_access"         // 该账号没绑 OA:重试无意义
	CodeIdentityUnavailable = "identity_unavailable" // 身份服务不可达:稍后可重试
	CodeOAError             = "oa_error"             // OA 自己拒绝或查不到
	CodeUnknownTool         = "unknown_tool"         // 客户端与服务端的工具清单漂移
)

// Run 执行一次 OA 只读查询。
//
// 顺序是这个函数里唯一要紧的事:**先过 Gate,再谈别的**。Gate 之前不做任何网络
// 动作,连令牌都不取。
func (t Tool) Run(ctx context.Context, raw json.RawMessage, tctx *tool.Context, _ chan<- tool.Event) (tool.Result, error) {
	if t.cfg.Gate != nil {
		toolUseID := ""
		if tctx != nil {
			toolUseID = tctx.ToolUseID
		}
		if err := t.cfg.Gate(ctx, t.desc.name, toolUseID); err != nil {
			return errorResult(err.Error()), nil
		}
	}

	args, err := t.parseArgs(raw)
	if err != nil {
		return tool.Result{}, err
	}

	body, err := json.Marshal(invokeRequest{Tool: t.desc.remote, Args: args})
	if err != nil {
		return tool.Result{}, fmt.Errorf("build oa request: %w", err)
	}

	status, payload, err := t.post(ctx, body)
	if err != nil {
		return tool.Result{}, err
	}
	if status < 200 || status >= 300 {
		return errorResult(fmt.Sprintf("OA 服务返回 HTTP %d：%s", status, upstreamMessage(payload))), nil
	}

	var env invokeEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return tool.Result{}, fmt.Errorf("parse oa response: %w", err)
	}
	if !env.State {
		return errorResult(businessFailure(env)), nil
	}
	if len(env.Data) == 0 && len(env.Images) == 0 {
		return errorResult("OA 未返回任何内容。"), nil
	}
	// data 原样透出,不重新编码:服务端给的就是给模型看的那份 JSON(键是中文),
	// 再 marshal 一遍只会引入转义差异,不会增加任何信息。
	content := make([]tool.ResultContent, 0, 1+len(env.Images))
	if len(env.Data) > 0 {
		content = append(content, tool.ResultContent{Type: tool.ResultContentTypeText, Text: string(env.Data)})
	}
	content = append(content, imageContent(env.Images)...)
	return tool.Result{Content: content}, nil
}

// imageContent 把信封里的原图变成工具结果的 image 块。
//
// # 为什么能这么做
//
// OpenAI 的 tool 角色消息只收文本,收不了图。但引擎的 openai provider 有现成的
// 变通(convertToolMessage):它把工具结果里的图片收集起来,在所有 tool 消息之后
// 合成一条 user 消息用 image_url 发出去——正是内网视觉模型认的形状。内置的 Read
// 工具读图片走的就是这条路。所以这里零引擎改动。
//
// # 为什么值得这么做
//
// 服务端也能自己 OCR 完把文字给我们(MCP 那条老路就是),但那样保密数据就有**两个
// 出口**:工具结果(受本地模型闸门管)和那条独立配置的 VL 调用(受 VL_MODEL 管)。
// 后者配错就是一条没有报错、没有日志的外泄通路。把原图交给会话模型,出口就只剩
// 一个,而它已经被闸门锁死。
//
// 坏掉的条目静默跳过:一张图解不开不该让整次查询失败,文字部分通常已经够用了。
func imageContent(images []envelopeImage) []tool.ResultContent {
	out := make([]tool.ResultContent, 0, len(images))
	for _, img := range images {
		if len(out) >= maxImages {
			break
		}
		data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(img.Data))
		if err != nil || len(data) == 0 || len(data) > maxImageBytes {
			continue
		}
		mediaType := strings.TrimSpace(img.MediaType)
		if !strings.HasPrefix(mediaType, "image/") {
			continue
		}
		out = append(out, tool.ResultContent{
			Type:  tool.ResultContentTypeImage,
			Image: &tool.ResultImage{MediaType: mediaType, Data: data},
		})
	}
	return out
}

// parseArgs 把模型给的入参收敛成字符串表,并挡掉 schema 里没有的键。
//
// 挡掉多余键不是洁癖:入参会原样转给 OA,放任模型自造字段等于给了它一条我们没有
// 审视过的通路(比如某个 OA 接口恰好认某个参数名)。
func (t Tool) parseArgs(raw json.RawMessage) (map[string]string, error) {
	var in map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, fmt.Errorf("parse %s input: %w", t.desc.name, err)
		}
	}
	out := make(map[string]string, len(t.desc.args))
	for _, a := range t.desc.args {
		v, ok := in[a.name]
		if !ok || v == nil {
			if a.required {
				return nil, fmt.Errorf("%s is required", a.name)
			}
			continue
		}
		s := strings.TrimSpace(asString(v))
		if s == "" {
			if a.required {
				return nil, fmt.Errorf("%s is required", a.name)
			}
			continue
		}
		out[a.name] = s
	}
	return out, nil
}

// asString 接受模型可能给出的几种标量写法。流程号/文档号常被写成数字而不是字符串,
// 为此让整个调用失败纯属自找麻烦。
func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		// 流程号是整数;用 %v 会得到 1.234567e+06 这种科学计数法。
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%v", x)
	case bool:
		return fmt.Sprintf("%t", x)
	default:
		return ""
	}
}

// post 发一次请求并读回状态码与正文。401 时先请调用方强制刷新令牌,再重试一次
// ——桌面会话可以开很久,访问令牌过期是常态而不是异常。
func (t Tool) post(ctx context.Context, body []byte) (int, []byte, error) {
	status, payload, err := t.postOnce(ctx, body)
	if err != nil || status != http.StatusUnauthorized || t.cfg.OnUnauthorized == nil {
		return status, payload, err
	}
	t.cfg.OnUnauthorized()
	return t.postOnce(ctx, body)
}

func (t Tool) postOnce(ctx context.Context, body []byte) (int, []byte, error) {
	token, err := t.cfg.Token()
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.cfg.Endpoint+"/invoke", bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("build oa request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := t.cfg.Client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("oa request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return 0, nil, fmt.Errorf("read oa response: %w", err)
	}
	return resp.StatusCode, payload, nil
}

// businessFailure 拼出 State=false 时给模型看的那句话。服务端的 Message 已经是
// 面向用户的完整说明(它才知道断在哪一环),这里只在它缺席时按 Code 兜底。
func businessFailure(env invokeEnvelope) string {
	if m := strings.TrimSpace(env.Message); m != "" {
		return m
	}
	switch env.Code {
	case CodeNoOAAccess:
		return "无权访问 OA 系统。本助手只能查你本人在 OA 里的数据，请联系 OA 管理员为你的账号绑定 OA 工号后再试。"
	case CodeIdentityUnavailable:
		return "身份服务暂时不可用，请稍后重试。"
	case CodeUnknownTool:
		return "OA 服务不认识这个工具，可能是客户端与服务端版本不一致。"
	default:
		return "OA 查询失败（服务端未说明原因）。"
	}
}

// upstreamMessage 从非 2xx 的正文里挑一句能给人看的。
func upstreamMessage(payload []byte) string {
	var probe struct {
		Message string `json:"message"`
		Error   any    `json:"error"`
	}
	if err := json.Unmarshal(payload, &probe); err == nil {
		if m := strings.TrimSpace(probe.Message); m != "" {
			return truncate(m)
		}
		switch e := probe.Error.(type) {
		case string:
			if m := strings.TrimSpace(e); m != "" {
				return truncate(m)
			}
		case map[string]any:
			if m, ok := e["message"].(string); ok && strings.TrimSpace(m) != "" {
				return truncate(strings.TrimSpace(m))
			}
		}
	}
	return truncate(fallback(collapse(string(payload)), "(无返回内容)"))
}

func errorResult(text string) tool.Result {
	return tool.Result{IsError: true, Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: text}}}
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

func fallback(s, or string) string {
	if s == "" {
		return or
	}
	return s
}

// truncate 按 rune 截断,避免把一个中文字切成半个。
func truncate(s string) string {
	if len(s) <= maxErrorText {
		return s
	}
	r := []rune(s)
	for len(string(r)) > maxErrorText {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// ErrGateClosed 是 Gate 拒绝时的兜底措辞,供没有更具体说明的调用方复用。
var ErrGateClosed = errors.New("OA 数据只能在本地模型会话中读取。")
