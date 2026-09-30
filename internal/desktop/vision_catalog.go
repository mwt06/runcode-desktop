package desktop

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const visionCatalogTTL = time.Minute

type visionCatalog struct {
	models   map[string]PassportModel
	defaults []PassportModel
	at       time.Time
}

func visionCatalogKey(scope modelReference, actor string) string {
	scope.Name = ""
	return digestVision([]any{scope, actor})
}

// Catalogs are immutable snapshots. Publish under the requesting identity, never
// whichever account happens to be logged in when a slow response arrives.
func (a *App) rememberVisionCatalog(scope modelReference, actor string, models []PassportModel) {
	next := visionCatalog{models: make(map[string]PassportModel, len(models)), at: time.Now()}
	for _, m := range models {
		next.models[m.ID] = m
		if m.VisionDefault {
			next.defaults = append(next.defaults, m)
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.visionModels == nil || len(a.visionModels) >= 64 {
		a.visionModels = map[string]visionCatalog{}
	}
	a.visionModels[visionCatalogKey(scope, actor)] = next
}

func (a *App) platformVisionCatalog(ctx context.Context, scope modelReference, actor string) (visionCatalog, error) {
	if err := ctx.Err(); err != nil {
		return visionCatalog{}, err
	}
	if !a.tokens.LoggedIn() {
		return visionCatalog{}, errors.New("跟随平台识图需要登录通行证，或手动选择自定义识图模型")
	}
	if a.visionAccountIdentity() != actor {
		return visionCatalog{}, errors.New("加载默认识图模型期间账号已变化，请重试")
	}
	if scope != platformModelRef(scope.TenantID, "") {
		return visionCatalog{}, errors.New("会话所属平台已变化，请重新打开会话")
	}
	key := visionCatalogKey(scope, actor)
	a.mu.Lock()
	entry, ok := a.visionModels[key]
	a.mu.Unlock()
	if ok && time.Since(entry.at) < visionCatalogTTL {
		return entry, nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if _, err := a.passportModelsContext(lookupCtx, scope.TenantID); err != nil {
		return visionCatalog{}, fmt.Errorf("获取平台默认识图模型失败: %w", err)
	}
	if err := lookupCtx.Err(); err != nil {
		return visionCatalog{}, err
	}
	if a.visionAccountIdentity() != actor {
		return visionCatalog{}, errors.New("加载默认识图模型期间账号已变化，请重试")
	}
	a.mu.Lock()
	entry, ok = a.visionModels[key]
	a.mu.Unlock()
	if !ok {
		return visionCatalog{}, errors.New("平台模型目录已变化，请重试")
	}
	return entry, nil
}

func selectPlatformVisionModel(defaults []PassportModel) (PassportModel, error) {
	if len(defaults) == 0 {
		return PassportModel{}, errors.New("平台未配置默认识图模型，或当前租户无权使用；请联系管理员或手动选择识图模型")
	}
	if len(defaults) != 1 {
		return PassportModel{}, errors.New("平台配置了多个默认识图模型，请联系管理员修正")
	}
	m := defaults[0]
	if m.ID == "" || !m.VisionDefault || m.SupportsImages == nil || !*m.SupportsImages {
		return PassportModel{}, errors.New("平台默认识图模型必须明确支持图片")
	}
	return m, nil
}

// Explicit user preferences never silently fall back to another connection.
func (a *App) visionTarget(ctx context.Context, sessionID, bridge, actor string, source modelReference, stored desktopConfig) (modelReference, error) {
	if stored.Vision.Disabled {
		return modelReference{}, errors.New("已关闭识图兜底，当前模型仅支持文本；请在设置中选择跟随平台或指定识图模型")
	}
	if stored.Vision.DefaultModel != nil {
		return *stored.Vision.DefaultModel, nil
	}
	scope := source
	scope.Name = ""
	if source.Kind != "platform" {
		a.mu.Lock()
		entry := a.entryLocked(sessionID)
		tenant, bound := "", entry != nil
		if bound {
			tenant = entry.tenantID
		}
		a.mu.Unlock()
		if !bound {
			return modelReference{}, errors.New("会话没有平台租户绑定，请重新打开会话或手动选择识图模型")
		}
		scope = modelReference{Kind: "platform", Bridge: bridge, TenantID: tenant}
	}
	catalog, err := a.platformVisionCatalog(ctx, scope, actor)
	if err != nil {
		return modelReference{}, err
	}
	model, err := selectPlatformVisionModel(catalog.defaults)
	if err != nil {
		return modelReference{}, err
	}
	scope.Name = model.ID
	if override := imageOverride(stored, scope); override != nil && !*override {
		return modelReference{}, fmt.Errorf("平台默认识图模型 %s 已被本机标为仅文本，请恢复其图片能力或手动选择其他识图模型", model.ID)
	}
	return scope, nil
}
