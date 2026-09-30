package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/wt68/runcode/internal/vision"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	"gitlab.ouc-online.com.cn/aibase/agentloop/imageinput"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
)

func digestVision(value any) string {
	b, _ := json.Marshal(value)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Only an identity fingerprint is kept. This is cache partitioning, not JWT
// authentication; the gateway still authenticates the original bearer token.
func tokenIdentity(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) == 3 {
		b, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err == nil {
			var claims struct {
				Sub    string `json:"sub"`
				Issuer string `json:"iss"`
			}
			if json.Unmarshal(b, &claims) == nil && claims.Sub != "" {
				return digestVision([]string{claims.Issuer, claims.Sub})
			}
		}
	}
	return ""
}

func (a *App) visionAccountIdentity() string {
	a.tokens.mu.Lock()
	token, identity := a.tokens.ts.AccessToken, a.tokens.identity
	a.tokens.mu.Unlock()
	if key := tokenIdentity(token); key != "" {
		return key
	}
	return digestVision(identity)
}

func (a *App) modelImageSupport(ctx context.Context, ref modelReference, model string, stored desktopConfig) *bool {
	if ref.Kind == "custom" {
		for _, m := range stored.CustomModels {
			if m.Name == ref.Name && m.Model == model {
				return m.SupportsImages
			}
		}
		return nil
	}
	if ref.Kind != "platform" {
		return nil
	}
	ref.Name = model
	if v := imageOverride(stored, ref); v != nil {
		return v
	}
	keyRef := ref
	keyRef.Name = ""
	key := digestVision([]any{keyRef, a.visionAccountIdentity()})
	a.mu.Lock()
	models, known := a.visionModels[key]
	value := models.models[model].SupportsImages
	a.mu.Unlock()
	if !known && ref.Bridge == platformModelRef(ref.TenantID, model).Bridge {
		lookupCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		_, _ = a.passportModelsContext(lookupCtx, ref.TenantID)
		cancel()
		a.mu.Lock()
		value = a.visionModels[key].models[model].SupportsImages
		a.mu.Unlock()
	}
	return value
}

func (a *App) configureImages(sctx host.SessionContext, cfg engine.Config, opts *engine.Options) {
	a.mu.Lock()
	initialRef := a.pendingVisionRef
	a.mu.Unlock()
	bridge := platformModelRef("", "").Bridge
	observer := opts.LLMRequestObserver
	initialSupport := a.modelImageSupport(context.Background(), initialRef, cfg.Model, loadRawConfig())
	opts.Images = &imageinput.Options{Scope: sctx.ID, Store: vision.NewStore(cfg.CWD, sctx.ID), Snapshot: func(ctx context.Context, model, turnID string) (imageinput.Route, error) {
		if err := ctx.Err(); err != nil {
			return imageinput.Route{}, err
		}
		stored := loadRawConfig()
		ref := a.visionReference(sctx.ID, initialRef)
		support := a.modelImageSupport(ctx, ref, model, stored)
		if support == nil && model == cfg.Model && ref.Kind == "custom" {
			exists := false
			for _, m := range stored.CustomModels {
				if m.Name == ref.Name && m.Model == model {
					exists = true
					break
				}
			}
			if !exists {
				support = initialSupport
			}
		}
		route := imageinput.Route{TextOnly: support != nil && !*support}
		if !route.TextOnly {
			return route, nil
		}
		account := a.visionAccountIdentity()
		var target modelReference
		var targetErr error
		if a.oaLockedModel(sctx.ID) != "" {
			targetErr = errors.New("此会话已读取 OA 数据，不能发送给另一条识图连接；请配置支持图片的 OA 内网模型")
		} else {
			target, targetErr = a.visionTarget(ctx, sctx.ID, bridge, account, ref, stored)
		}
		if targetErr != nil {
			// Do not fail an otherwise valid text-only turn. The router checks
			// this when an image actually needs analysis, before any cache hit.
			route.Check = func(ctx context.Context) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				return targetErr
			}
			return route, nil
		}
		var profile CustomModel
		if target.Kind == "custom" {
			for _, m := range stored.CustomModels {
				if m.Name == target.Name {
					profile = m
					break
				}
			}
		}
		codexActor := ""
		if isCodexProfile(profile.Provider) && normalizeCodexAuthMode(profile.Provider, profile.AuthMode) != codexAuthAPIKey {
			codexActor = a.visionCodexIdentity()
		}
		route.Key = digestVision([]any{target, profile, account, codexActor, stored.VisionRevision, "vision-v1"})
		route.Model = target.Name
		route.Check = func(ctx context.Context) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if a.oaLockedModel(sctx.ID) != "" {
				return errors.New("此会话已读取 OA 数据，不能发送给另一条识图连接；请配置支持图片的 OA 内网模型")
			}
			if target.Kind == "platform" && (a.visionAccountIdentity() != account || !a.tokens.LoggedIn()) {
				return errors.New("识图账号已变化，请重试")
			}
			if codexActor != "" && a.visionCodexIdentity() != codexActor {
				return errors.New("ChatGPT 识图账号已变化，请重试")
			}
			return nil
		}
		// Resolve only when a picture is actually needed. The configuration is
		// this turn's immutable snapshot; catalog IO shares the caller's deadline.
		route.Analyze = func(ctx context.Context, q imageinput.Query) (imageinput.Answer, error) {
			ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
			defer cancel()
			if err := route.Check(ctx); err != nil {
				return imageinput.Answer{}, err
			}
			targetCfg, err := a.resolveVisionModelFrom(ctx, target, stored)
			if err != nil {
				return imageinput.Answer{}, err
			}
			provider, closeProvider, err := a.visionProvider(targetCfg, target, stored, route.Check)
			if err != nil {
				return imageinput.Answer{}, err
			}
			defer closeProvider()
			client := vision.Client{Provider: provider, Model: targetCfg.Model, Check: route.Check, Refresh: targetCfg.OnUnauthorized}
			if observer != nil {
				client.Observe = func(req llm.Request) { observer("image_analysis", turnID, req) }
			}
			return client.Analyze(ctx, q)
		}

		return route, nil
	}}
}
