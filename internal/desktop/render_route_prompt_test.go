package desktop

import (
	"strings"
	"testing"

	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
)

func TestRenderRoutePromptMergesIntoSessionConfig(t *testing.T) {
	app := New(&recordingSink{})
	ws := t.TempDir()
	cfg := engine.Config{
		CWD:                ws,
		PermissionMode:     "interactive",
		SystemPromptAppend: "caller-sentinel",
	}
	sctx := host.SessionContext{
		ID:       "render-route",
		Approver: host.NewAsyncApprover(func(string, any) {}, ws),
		Emit:     func(string, any) {},
	}
	var opts engine.Options
	app.configureSession(sctx, &cfg, &opts)

	// 通过真实会话配置 hook 检查合并结果，避免只测试常量却漏掉接线。
	for _, want := range []string{
		"caller-sentinel",
		localBrowserPrompt,
		renderRoutePrompt,
		"Mermaid",
		"SVG",
		"HTML",
		"Do not create an HTML file only to preview Mermaid",
	} {
		if !strings.Contains(cfg.SystemPromptAppend, want) {
			t.Errorf("SystemPromptAppend missing %q", want)
		}
	}
	for _, unwanted := range []string{"image_generate", "修改生图设置"} {
		if strings.Contains(cfg.SystemPromptAppend, unwanted) {
			t.Errorf("non-image session unexpectedly contains %q", unwanted)
		}
	}
}

func TestImageGenerationRoutePromptRequiresRegisteredTool(t *testing.T) {
	cfg := engine.Config{}
	appendRenderRoutePrompts(&cfg, false)
	if strings.Contains(cfg.SystemPromptAppend, "image_generate") {
		t.Fatalf("generic route prompt leaked image_generate: %s", cfg.SystemPromptAppend)
	}
	if !strings.Contains(cfg.SystemPromptAppend, "Mermaid") {
		t.Fatalf("generic route prompt missing Mermaid: %s", cfg.SystemPromptAppend)
	}

	cfg = engine.Config{}
	appendRenderRoutePrompts(&cfg, true)
	for _, want := range []string{
		"Mermaid",
		"image_generate",
		"Do not use image generation for ordinary flowcharts",
		"Do not substitute SVG",
		"修改生图设置",
	} {
		if !strings.Contains(cfg.SystemPromptAppend, want) {
			t.Errorf("image route prompt missing %q", want)
		}
	}
}
