# 设计：桌面端对接 OUCOnline 通行证 + Java 中间层（ouconline-ai-bridge）

日期：2026-07-14
状态：已与需求方对齐，待实施
范围：三个子项目 —— Java 中间层（新建）、Passport 客户端注册（配置）、runcode 桌面端对接

## 背景与目标

runcode 桌面端（Wails，Windows 为主）目前没有用户/登录概念，模型凭证靠手动填写。目标：

1. 用户用 **OUCOnline.AI.Passport 通行证**（IdentityServer4，OAuth2/OIDC）登录桌面端；
2. 登录后模型调用统一经 **新建的 Java 中间层** 转发到 **OUCOnline.AI.Core**（OpenAI 兼容网关，内部完成限流/计费/供应商密钥管理/上游模型调用）；
3. 保留并升级"自定义模型"能力：用户可自行添加直连模型接入点，与平台模型并存。

### 既有事实（调研结论）

- Passport 是标准 IdentityServer4（.NET 5）：`/connect/authorize`、`/connect/token`、`/connect/userinfo`、`/connect/endsession`；JWT 有效期 3600s，`AllowOfflineAccess` 可发 refresh token。JWT claims 含 `TenantId`、`UserId`、`UserName`、`Name`、`Nickname`、`Avatar` 等。现有 5 个 Client 均为 Implicit 流，无桌面端可用的 Client。
- AI.Core 的 `/v1/*` 不校验 Passport JWT，只认两种 Bearer（`OpenAICommonService.AnalyzeAuthorization`）：
  - `Bearer tenant-xxx`（租户密钥，深度防御过滤器校验）；
  - `Bearer {JSON}`（`TenantId/AppId/DependentType/DependentId/DependentName/RelationKeyId/RelationKeyType`），**信任上游网关已做主鉴权**。
- AI.Core 的 `/v1/Chat/Completions` 内部已完成：限流 → token 估算 → 计费配置 → 额度检测 → 按模型解析上游 baseUrl/apiKey → 调用上游 → 输出归一化（`<think>` 标签、finish_reason 映射等）→ 会话日志（按 TenantId/AppId/DependentId 记账）→ 扣额度。**中间层不得重复实现模型调用。**
- AI.Core 出错时返回 HTTP 200 + `finish_reason=stop` 的错误文本消息（非 HTTP 错误码）。
- 桌面端 openai provider 的 bearer 在构造时固定（`pkg/llm/providers/openai/client.go`），不支持运行中换 token。
- 桌面端已有可复用基础：DPAPI 凭证加密（`internal/desktop/store.go` + `secret_windows.go`；macOS/Linux 目前 no-op）、Wails 方法绑定 + 事件推送、回环 HTTP 服务器范式（`preview_server.go`）、`BrowserOpenURL`。
- `internal/webclient` 的加固 client 拒连回环/内网地址，**不能**用于调 Bridge。

## 总体架构

```
┌─────────────────────────────────────────────────────────────────┐
│ ① 登录（一次性）                                                  │
│ 桌面端 ─拉起系统浏览器─▶ Passport /connect/authorize (PKCE)        │
│   ▲                        用户任意方式登录（密码/验证码/联邦）      │
│   └─127.0.0.1 回环回调收 code ─▶ POST /connect/token              │
│      得 access_token(1h) + refresh_token，DPAPI 加密落盘           │
├─────────────────────────────────────────────────────────────────┤
│ ② 日常调用                                                        │
│ 桌面端 ─Bearer <Passport JWT>─▶ ouconline-ai-bridge (Java, 新建)  │
│              校验 JWT(JWKS) → 构造 JSON Bearer + 注入 user 字段    │
│                     └──────▶ AI.Core /v1/*（限流/计费/调上游模型）  │
└─────────────────────────────────────────────────────────────────┘
```

关键决策（已确认）：

| 决策点 | 结论 |
|---|---|
| Java 层职责 | 纯"身份翻译代理"：校验 Passport JWT → 构造 JSON Bearer → 转发 AI.Core；无状态、无数据库 |
| Java 层调 AI.Core 的身份 | JSON Bearer 按用户构造：`DependentType=User`、`DependentId=UserId`、`DependentName=urlencode(Name)`、`TenantId` 取自 JWT claims（计费/归因到人） |
| 桌面登录交互 | 系统浏览器 + authorization_code + PKCE + 回环回调（密码不经过桌面应用，联邦/验证码登录天然可用） |
| 桌面配置形态 | 登录为主：登录后模型列表来自 Bridge `/v1/models`；另提供"自定义模型"直连能力 |
| Java 技术栈 | JDK 21 + Spring Boot 4.x + Maven，手写代理服务（不用 Spring Cloud Gateway，不做仅令牌置换方案） |
| Java 命名 | 遵循 Java 标准命名，不沿用 .NET 风格（见子项目 A） |
| 实施顺序 | Bridge → Passport 客户端注册 → 桌面端 |

## 子项目 A：ouconline-ai-bridge（Java 中间层，新建）

- **位置**：`D:\ouconline\ouconline-ai-bridge`
- **坐标与包名**：`groupId=cn.ouconline.ai`，`artifactId=ouconline-ai-bridge`；基础包 `cn.ouconline.ai.bridge`，子包 `config`（安全/属性）、`proxy`（转发）、`me`（用户信息）
- **栈**：JDK 21（虚拟线程）+ Spring Boot 4.x + Maven；单模块；无数据库（所有租户/计费状态在 AI.Core，Bridge 天然可水平扩展）

### 组件

1. `config.SecurityConfig`
   - `oauth2ResourceServer().jwt()`，`spring.security.oauth2.resourceserver.jwt.issuer-uri` 指向 Passport（经发现文档/JWKS 自动验签 + 有效期校验）；
   - `/v1/**`、`/api/**` 全部要求认证；健康检查端点放行。
2. `config.BridgeProperties`
   - `bridge.aicore.base-url`（AI.Core 地址）、连接/读超时、SSE 空闲超时。
3. `proxy.ProxyController` + `proxy.ProxyService`
   - `GET /v1/models`、`POST /v1/chat/completions`：从 JWT claims 取 `TenantId/UserId/Name`，构造
     `Authorization: Bearer {"TenantId":"…","DependentType":"User","DependentId":"<UserId>","DependentName":"<urlencode(Name)>"}` 转发 AI.Core；
   - `stream=true` 时 SSE 逐块透传（虚拟线程 + 流拷贝，禁止整响应缓冲）；
   - 请求体 `user` 字段注入 `UserId`（AI.Core 记入会话日志 UserId）。
4. `me.MeController`
   - `GET /api/me`：直接从 JWT claims 返回 `{userId, userName, name, nickname, avatar, tenantId}`（不回源 Passport userinfo）。
5. `oauth.OAuthRelayController`（回调中转，已实现）
   - `GET /oauth/callback`（匿名）：Passport 只注册本端点这一个 redirect_uri；桌面端把临时回环端口编进 state（`<nonce>.<port>`），本端点校验端口（1024-65535）后 302 回跳 `http://127.0.0.1:<port>/callback` 并原样透传 query。跳转目标主机/路径写死，仅端口可变，不构成开放重定向；PKCE 保证经手的授权码无法被兑换。

### 错误语义

- 无 token / 过期 / 验签失败 → 标准 401（桌面端据此触发刷新重试）；
- AI.Core 的响应原样透传（包括其 200+错误文本的现状），Bridge 不二次包装；
- AI.Core 不可达/超时 → 502/504 + JSON 错误体（与模型输出可区分）。

### 部署与网络边界

- Dockerfile + K8s yaml（仿 AI.Core `yamls/` 风格）；
- **必须部署在与 AI.Core 同一受信网络**（JSON Bearer 是信任模式）；AI.Core 侧可对 Bridge 出口 IP 配白名单兜底；Bridge 对公网只暴露经网关/Ingress 的 HTTPS。

### 测试

- JUnit + MockWebServer 假扮 AI.Core（含 SSE 流式断言：分块到达、顺序、`data: [DONE]` 终止）；
- `spring-security-test` 的 `jwt()` postProcessor 构造带 claims 的令牌；
- 覆盖：JWT 缺失/过期 → 401；claims → JSON Bearer 构造（含中文名 urlencode）；`user` 字段注入；非流式/流式转发。

## 子项目 B：Passport 客户端注册（配置为主，零代码）

在 IdentityServer 配置库新增 Client（Skoruba Admin 手工添加，或 `SeedData` 补种子）：

- `ClientId = runcode-desktop`；
- `AllowedGrantTypes = authorization_code`，`RequirePkce = true`，`RequireClientSecret = false`（公共客户端）；
- `RedirectUris`：只注册 Bridge 的中转端点 `https://<Bridge域名>/oauth/callback`（本地联调另加 `http://localhost:8080/oauth/callback`）。桌面端任意空闲回环端口都可用——端口经 state 传给 Bridge 中转回跳，Passport 侧无需注册回环 URI；
- `AllowOfflineAccess = true`（refresh token）；
- `AllowedScopes = openid profile offline_access passportapi`（**必须含 `passportapi` 这类带用户声明的 API scope**——IdentityServer4 中 `TenantId/UserId` 等 UserClaims 只随 ApiScope 进入 access token，身份资源声明只进 id_token/userinfo；Bridge 从 access token 读取 claims，缺失时返回 403）；
- Access token 有效期维持默认 3600s。

## 子项目 C：桌面端（runcode_desktop）

### 核心模块小扩展（`pkg/llm/providers/openai`）

- `Options` 增加 `TokenSource func() (string, error)`；
- `httpClient` 每次请求优先从 `TokenSource` 取 bearer，为空/未设置则回落现有静态 `APIKey`/`AuthToken`；
- 向后兼容，改动集中，是登录 token 在长会话中自动续期的基础。

### Go 侧（`internal/desktop/passport.go` + `internal/desktop/oauth.go`）

- `PassportLogin()`：生成 PKCE verifier/challenge + nonce → 绑定任意空闲回环端口，`state = <nonce>.<端口>`，`redirect_uri = https://<Bridge域名>/oauth/callback` → 系统浏览器打开 authorize URL → Bridge 中转 302 回跳本地回环 → 校验 state、收 code → `POST /connect/token` 换令牌 → DPAPI 加密存入 `desktop.json`（复用 `protectSecret`）→ 发 `passport:changed` 事件。整体超时 5 分钟，可取消；回调页向浏览器返回"登录成功，请返回应用"。
- **令牌管理器**：内存持有 access/refresh/expiry；过期前或收 401 时用 refresh token 静默续期；续期失败 → 置登出态 + `passport:changed`。同一对象即 LLM 引擎的 `TokenSource`。
- `PassportStatus()`：登录态 + 用户信息（来自 Bridge `/api/me`）；
- `PassportLogout()`：清本地凭证（不调 endsession——桌面登出不应顺带登出浏览器 SSO）；
- `PassportModels()`：调 Bridge `/v1/models` 返回平台模型列表；
- 调 Bridge/Passport 用普通 `http.Client`（**不用** `webclient.New`，它拒连内网/回环）。

### 前端

- `StartForm` 登录优先：未登录显示"通行证登录"主按钮；登录后显示用户名/头像 + 模型下拉（平台模型 + 自定义模型合并，分组显示）；
- **自定义模型管理**：设置页新增区块，增删 `{显示名, baseURL, APIKey, 模型ID}`（APIKey 走 DPAPI）；选自定义模型 → 旧直连方式；选平台模型 → baseURL=Bridge、凭证=自动管理 JWT；
- 设置页账号区块：登录用户信息 + 登出；
- `bridge.ts`/`wails.d.ts` 补方法与 `passport:changed` 事件订阅。

## 安全要点

- PKCE + state 双防护；回环服务器只监听 `127.0.0.1`、一次性使用、校验 state 后立即关闭；
- 令牌仅以 DPAPI 加密串落盘（Windows）；**本期只保证 Windows 的安全存储**，macOS/Linux 的 Keychain/Secret Service 后补（现为 no-op，不落盘明文）；
- refresh token 泄漏面控制：不写日志、不进前端（前端只拿登录态与用户信息，令牌全留在 Go 侧）。

## 错误处理

| 场景 | 行为 |
|---|---|
| 用户未完成浏览器登录 | 5 分钟超时/手动取消，回环服务器关闭，状态回未登录 |
| refresh token 过期/吊销 | 模型调用报"请重新登录"，前端弹登录引导 |
| Bridge 不可达 | 明确网络错误提示（区别于模型错误） |
| AI.Core 200+错误文本 | 原样显示为助手消息（维持现状，后续可在 Bridge 侧识别优化） |

## 验证计划

- Bridge：单测全绿 + 本地起服务用 curl 带真实/伪造 JWT 验证 401/转发/SSE；
- 桌面端：Go 单测（PKCE、回调 server、令牌刷新状态机、TokenSource 注入）；`go -C cmd/runcode-desktop build ./...` + 核心 `go test -race ./...`；
- 端到端手动：登录 → 列模型 → 流式对话 → 令牌过期后自动续期 → 登出；
- 联调依赖：可访问的 Passport 与 AI.Core 环境（或本地按 dev 配置起 Passport）。

## 明确不做（本期）

- Bridge 不做限流/计费/审计（AI.Core 已有）；不做数据库；
- 不改 Passport 代码（只加 Client 配置）；
- 不支持 macOS/Linux 安全令牌存储（平台 secret 后补）；
- 桌面端不做多账号切换；
- 除 `/v1/models`、`/v1/chat/completions`、`/api/me` 外的端点（embeddings、images 等）暂不代理，按需追加。
