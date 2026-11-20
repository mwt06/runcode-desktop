package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/wt68/runcode/internal/imagegentool"
	"github.com/wt68/runcode/internal/localusage"
)

// 生图请求：发往业务网关 POST {Bridge}/t/{tenant}/v1/Images/Generations（设计 6.2）。
//
// 任何已经发出的 POST 都不重试——401/403/429/5xx、超时、断连、空/多图都不重发，
// 也不走目录那套「401 刷新令牌后重发」。发出前可以正常取得/刷新令牌。错误只按网关
// 自己声明的错误码分类；Core 的原文、HTML、堆栈一律不回显，也不从文字里猜额度/限流/
// 内容拒绝。

var imageGenerationPostTimeout = 180 * time.Second

const imageGenerationResponseLimit = 32 << 20

const (
	imageGenerationMsgUnknown   = "生成结果无法确认，可能已生成或计费，请勿重复提交；如需核查请联系管理员"
	imageGenerationMsgGeneric   = "生成服务未返回可确认的结果，可能已生成或计费，请联系管理员核查"
	imageGenerationMsgCancelled = "已停止等待；请求已发出，上游可能已生成或计费"
)

// bridgeImageErrors 是 Bridge 自身可证明的拒绝（设计 6.3）：请求从未到达 Core。
// 每条只在声明的 HTTP 状态下接受。
var bridgeImageErrors = map[string]struct {
	status  int
	message string
}{
	"auth":              {http.StatusUnauthorized, "通行证登录已失效，本次未生成图片；请重新登录后再提出生成要求"},
	"forbidden":         {http.StatusForbidden, "当前账号或租户无权使用该生图模型，本次未生成图片"},
	"invalid_input":     {http.StatusBadRequest, "生成参数不被接受（如尺寸或提示词），本次未生成图片"},
	"model_unavailable": {http.StatusServiceUnavailable, "该生图模型当前不可用，本次未生成图片；请在设置中重新选择"},
}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

type imageGenerationWireRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	N              int    `json:"n"`
	Size           string `json:"size,omitempty"`
	ResponseFormat string `json:"response_format"`
}

type imageGenerationWireImage struct {
	URL           string `json:"url,omitempty"`
	B64JSON       string `json:"b64_json,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

// imageGenerationWireResponse 对齐 Core 的 created／data[]。requestID 只来自
// Bridge 的 X-Request-ID，绝不从正文猜。
type imageGenerationWireResponse struct {
	Created   int64                      `json:"created"`
	Data      []imageGenerationWireImage `json:"data"`
	requestID string
}

// imageGenerationFailure 是已分类的非成功结局。仅当可证明请求未产出任何图时 Status=failed。
type imageGenerationFailure struct {
	Status    string
	Code      string
	Message   string
	RequestID string
	// SubmissionStatus 只表示本地是否进入 HTTP Do；不推断上游是否收到或扣费。
	SubmissionStatus string
}

func (f *imageGenerationFailure) Error() string { return f.Message }

// imageGenerationFailureOf 分类任意错误；无法分类的一律 unknown。
func imageGenerationFailureOf(err error) imageGenerationFailure {
	if err == nil {
		return imageGenerationFailure{}
	}
	var f *imageGenerationFailure
	if errors.As(err, &f) {
		return *f
	}
	return imageGenerationFailure{Status: "unknown", Code: "unknown_outcome", Message: imageGenerationMsgUnknown, SubmissionStatus: "unknown"}
}

func notSent(code, message string) error {
	return &imageGenerationFailure{Status: "failed", Code: code, Message: message, SubmissionStatus: "not_submitted"}
}

func bridgeRequestID(h http.Header) string {
	id := strings.TrimSpace(h.Get("X-Request-ID"))
	if requestIDPattern.MatchString(id) {
		return id
	}
	return ""
}

func imageGenerationHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// 每次生图用新连接，避免复用连接带来的重放边角。
	transport.DisableKeepAlives = true
	return &http.Client{
		Transport: transport,
		// 绝不跟随跳转：Bearer 可能被带到别处，且已跳转的 POST 无法确认结局。
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// imageGenerationSendSnapshot 是同一把 tokenManager 锁下取得的发送身份与本地统计归属。
// 发送结束后不得重新读取登录态，否则账号切换会把结束事件错误归到新账号。
type imageGenerationSendSnapshot struct {
	Token string
	Epoch string
	Scope localusage.Scope
}

// prepareImageGenerationSend 完成所有本地拒绝条件后，返回不可变的 token／epoch／scope。
// Token() 可能刷新令牌；随后再次在锁下比对同一个 access token，避免把切换前后身份拼接。
func (a *App) prepareImageGenerationSend(snap imageGenerationSnapshot) (imageGenerationSendSnapshot, error) {
	if a.tokens == nil {
		return imageGenerationSendSnapshot{}, notSent("auth", "请先登录通行证；本次未生成图片")
	}
	token, err := a.tokens.Token()
	if err != nil {
		return imageGenerationSendSnapshot{}, notSent("auth", "请先登录通行证；本次未生成图片")
	}
	a.tokens.mu.Lock()
	defer a.tokens.mu.Unlock()
	currentToken, epoch := a.tokens.ts.AccessToken, a.tokens.identity
	identity := accountIdentityForToken(token, "")
	if token == "" || currentToken != token || !identity.Stable || identity.Digest != snap.Account || epoch != snap.Epoch {
		return imageGenerationSendSnapshot{}, notSent("auth", "会话账号已变化，本次未生成图片；请重新打开会话")
	}
	scope := (ScopeFreeze{AccountIssuer: identity.Issuer, AccountSub: identity.Subject, BridgeBase: snap.Bridge, TenantID: snap.Tenant}).toScope()
	return imageGenerationSendSnapshot{Token: token, Epoch: epoch, Scope: scope}, nil
}

// postImageGeneration 为冻结快照发送恰好一次生图请求。调用方必须先持久化 submitted 账本。
// beforeSend 在所有 notSent 检查后、HTTP Do 紧邻之前同步调用；它是图片用量的唯一起点。
func (a *App) postImageGeneration(ctx context.Context, snap imageGenerationSnapshot, in imagegentool.Input, beforeSend ...func(localusage.Scope)) (imageGenerationWireResponse, error) {
	if snap.Account == "" || snap.Bridge == "" || snap.Model == "" || (snap.ResponseFormat != "url" && snap.ResponseFormat != "b64_json") {
		return imageGenerationWireResponse{}, notSent("not_configured", "生图配置不完整，本次未生成图片")
	}
	if snap.Bridge != imageGenerationBridge() {
		return imageGenerationWireResponse{}, notSent("model_unavailable", "生图服务地址已变化，本次未生成图片；请重新打开会话")
	}
	prepared, err := a.prepareImageGenerationSend(snap)
	if err != nil {
		return imageGenerationWireResponse{}, err
	}
	token := prepared.Token
	body, _ := json.Marshal(imageGenerationWireRequest{Model: snap.Model, Prompt: in.Prompt, N: 1, Size: in.Size, ResponseFormat: snap.ResponseFormat})
	postCtx, cancel := context.WithTimeout(ctx, imageGenerationPostTimeout)
	defer cancel()
	endpoint := snap.Bridge + tenantPathPrefix(snap.Tenant) + "/v1/Images/Generations"
	req, err := http.NewRequestWithContext(postCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil || req.URL.Hostname() == "" || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
		return imageGenerationWireResponse{}, notSent("not_configured", "生图服务地址无效，本次未生成图片")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if err := postCtx.Err(); err != nil {
		return imageGenerationWireResponse{}, notSent("cancelled", "已停止等待，本次未提交生成请求")
	}
	// 这是唯一的提交边界：回调只接收冻结 Scope，且不持 token 锁，避免账本／
	// SQLite 写入与账号操作互锁。之后不再重读身份或中止提交，立即进入一次 Do；
	// 即使回调期间取消或切换账号，仍按本次冻结身份记 attempted。
	if len(beforeSend) > 0 && beforeSend[0] != nil {
		beforeSend[0](prepared.Scope)
	}
	resp, err := imageGenerationHTTPClient().Do(req)
	if err != nil {
		return imageGenerationWireResponse{}, interruptedOutcome(ctx, "")
	}
	defer func() { _ = resp.Body.Close() }()
	requestID := bridgeRequestID(resp.Header)
	raw, err := io.ReadAll(io.LimitReader(resp.Body, imageGenerationResponseLimit+1))
	if err != nil {
		return imageGenerationWireResponse{}, interruptedOutcome(ctx, requestID)
	}
	if len(raw) > imageGenerationResponseLimit {
		return imageGenerationWireResponse{}, &imageGenerationFailure{Status: "unknown", Code: "unknown_outcome", Message: imageGenerationMsgUnknown, RequestID: requestID, SubmissionStatus: "attempted"}
	}
	if resp.StatusCode != http.StatusOK {
		return imageGenerationWireResponse{}, classifyImageGenerationStatus(resp.StatusCode, raw, requestID)
	}
	var out imageGenerationWireResponse
	if json.Unmarshal(raw, &out) != nil || len(out.Data) != 1 || (out.Data[0].URL == "" && out.Data[0].B64JSON == "") {
		// 2xx 但不是恰好一张可用图：可能已生成并计费，只能记 unknown。
		return imageGenerationWireResponse{}, &imageGenerationFailure{Status: "unknown", Code: "unknown_outcome", Message: imageGenerationMsgUnknown, RequestID: requestID, SubmissionStatus: "attempted"}
	}
	out.requestID = requestID
	return out, nil
}

// interruptedOutcome 表示请求已发出（或可能已发出）但没有完整响应——禁止重发。
func interruptedOutcome(ctx context.Context, requestID string) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return &imageGenerationFailure{Status: "cancelled", Message: imageGenerationMsgCancelled, RequestID: requestID, SubmissionStatus: "attempted"}
	}
	return &imageGenerationFailure{Status: "unknown", Code: "unknown_outcome", Message: imageGenerationMsgUnknown, RequestID: requestID, SubmissionStatus: "attempted"}
}

func classifyImageGenerationStatus(status int, raw []byte, requestID string) error {
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	code := ""
	if json.Unmarshal(raw, &envelope) == nil {
		code = envelope.Error.Code
	}
	if known, ok := bridgeImageErrors[code]; ok && known.status == status {
		return &imageGenerationFailure{Status: "failed", Code: code, Message: known.message, RequestID: requestID, SubmissionStatus: "attempted"}
	}
	if code == "unknown_outcome" {
		return &imageGenerationFailure{Status: "unknown", Code: "unknown_outcome", Message: imageGenerationMsgUnknown, RequestID: requestID, SubmissionStatus: "attempted"}
	}
	// 其余（Core 不透明 500、Bridge 包装的内部 401/403、网关 HTML）都不能证明「什么都没生成」。
	return &imageGenerationFailure{Status: "unknown", Code: "generation_failed", Message: imageGenerationMsgGeneric, RequestID: requestID, SubmissionStatus: "attempted"}
}
