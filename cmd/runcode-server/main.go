// Command runcode-server 是 runcode 引擎的 HTTP 参考宿主（服务端交接骨架）。
//
// 它演示一个网络服务端接入 engine 的全部要点，且只使用 engine 模块的公开面
// （github.com/wt68/runcode/engine/...）与标准库：
//
//   - 命令面：POST /api/v1/rpc/{command}，请求/响应 JSON，错误 = protocol.Error；
//   - 事件面：GET /api/v1/sessions/{id}/events，SSE 推送 protocol.Envelope；
//   - 会话托管：engine/host.Manager（会话表、seq 信封、审批路由、限额、闲置回收）。
//
// 协议语义（回合状态机、信封、错误码、传输映射）见仓库 docs/protocol.md；
// 交接指引与待填清单见本目录 README.md（全部 HANDOFF 锚点在其中列出）。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/wt68/runcode/engine/host"
)

// config 是进程级配置，flag > env > 内置默认 三级解析。
//
// HANDOFF(config): 骨架用 env/flag 就够了；接入你们的配置中心/密钥管理时
// 整体替换 loadConfig 即可，config 结构体是唯一的消费面。
type config struct {
	// Addr 是监听地址（RUNCODE_ADDR，默认 :8787）。
	Addr string
	// Token 是 Bearer 鉴权令牌（RUNCODE_TOKEN）；空 = 不鉴权并在启动时打印警告。
	Token string
	// WorkspaceRoot 是每会话 workspace 的父目录（RUNCODE_WORKSPACE_ROOT）。
	// StartSession 只接受此目录内的 workspace（子目录名或其下绝对路径）。
	WorkspaceRoot string

	// 引擎参数：直接透传给 engine.Config（provider/model/凭证由服务器统一配置，
	// 不接受客户端指定——见 rpcStartSession）。
	Provider string
	Model    string
	BaseURL  string
	APIKey   string

	// MaxSessions/MaxTurns 映射 host.Limits（0 = 不限）。
	MaxSessions int
	MaxTurns    int
}

func loadConfig(args []string) (config, error) {
	fs := flag.NewFlagSet("runcode-server", flag.ContinueOnError)
	var cfg config
	fs.StringVar(&cfg.Addr, "addr", envOr("RUNCODE_ADDR", ":8787"), "listen address (env RUNCODE_ADDR)")
	fs.StringVar(&cfg.Token, "token", envOr("RUNCODE_TOKEN", ""), "bearer token, empty disables auth (env RUNCODE_TOKEN)")
	fs.StringVar(&cfg.WorkspaceRoot, "workspace-root", envOr("RUNCODE_WORKSPACE_ROOT", "./workspaces"), "parent dir of per-session workspaces (env RUNCODE_WORKSPACE_ROOT)")
	fs.StringVar(&cfg.Provider, "provider", envOr("RUNCODE_PROVIDER", ""), "LLM provider, e.g. anthropic/openai (env RUNCODE_PROVIDER)")
	fs.StringVar(&cfg.Model, "model", envOr("RUNCODE_MODEL", ""), "model name (env RUNCODE_MODEL)")
	fs.StringVar(&cfg.BaseURL, "base-url", envOr("RUNCODE_BASE_URL", ""), "provider base URL override (env RUNCODE_BASE_URL)")
	fs.StringVar(&cfg.APIKey, "api-key", envOr("RUNCODE_API_KEY", ""), "provider API key (env RUNCODE_API_KEY)")
	fs.IntVar(&cfg.MaxSessions, "max-sessions", envInt("RUNCODE_MAX_SESSIONS", 16), "max concurrently open sessions, 0 = unlimited (env RUNCODE_MAX_SESSIONS)")
	fs.IntVar(&cfg.MaxTurns, "max-turns", envInt("RUNCODE_MAX_TURNS", 4), "max concurrently running turns, 0 = unlimited (env RUNCODE_MAX_TURNS)")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	// workspace 根目录取绝对路径，后续所有“必须在 root 之下”的校验都以它为基准。
	abs, err := filepath.Abs(cfg.WorkspaceRoot)
	if err != nil {
		return config{}, fmt.Errorf("resolve workspace root: %w", err)
	}
	cfg.WorkspaceRoot = abs
	return cfg, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "runcode-server:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cfg, err := loadConfig(args)
	if err != nil {
		return err
	}
	logger := log.New(os.Stderr, "[runcode-server] ", log.LstdFlags)
	// HANDOFF(auth): 空 token = 不鉴权，只适合本机/可信内网调试；
	// 多用户认证（以及 per-user 的 engine.Config.TokenSource 注入）见 server.go 的锚点。
	if cfg.Token == "" {
		logger.Printf("警告: 未设置 RUNCODE_TOKEN，全部 API 不鉴权（仅限本机/可信内网调试）")
	}
	if err := os.MkdirAll(cfg.WorkspaceRoot, 0o755); err != nil {
		return fmt.Errorf("create workspace root: %w", err)
	}

	// hub 实现 host.Sink：Manager 的每条信封按 sessionId 分发到 SSE 订阅者。
	hub := newHub(logger.Printf)
	mgr := host.NewManager(host.Options{
		// DefaultBuild = engine.Build：真实 LLM 会话。测试注入 fake（见 fakes_test.go）。
		Build: host.DefaultBuild,
		Sink:  hub,
		Limits: host.Limits{
			MaxSessions:        cfg.MaxSessions,
			MaxConcurrentTurns: cfg.MaxTurns,
		},
	})
	srv := newServer(cfg, mgr, hub, logger)

	httpSrv := &http.Server{
		Addr:    cfg.Addr,
		Handler: srv.handler(),
		// 只设 ReadHeaderTimeout：SSE 是长连接，设置 WriteTimeout 会把事件流掐断。
		ReadHeaderTimeout: 10 * time.Second,
	}

	// 优雅停机：SIGINT/SIGTERM → 关会话（engine 资源落盘）→ 断开事件流 → HTTP Shutdown。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()
	logger.Printf("listening on %s (workspace root: %s)", cfg.Addr, cfg.WorkspaceRoot)

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}

	logger.Printf("shutting down ...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// 先 CloseAll（中断在跑的回合、按序关闭 engine 资源），再 dropAll 让所有
	// SSE handler 退出，最后 Shutdown 等 HTTP 连接排空——顺序反了 Shutdown 会
	// 被仍在阻塞的 SSE 长连接卡住。
	if err := mgr.CloseAll(shutdownCtx); err != nil {
		logger.Printf("close sessions: %v", err)
	}
	srv.hub.dropAll()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	logger.Printf("bye")
	return nil
}
