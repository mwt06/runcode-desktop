package desktop

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	modelCatalogTTL = time.Minute
	// Bridge 不可达时，会话构建可回落到同账号过期快照；超过此龄不再信任。
	modelCatalogStaleLimit = 24 * time.Hour
	// 拉取失败后这段时间内会话构建不再问 Bridge，直接走回落，避免再干等超时。
	modelCatalogRetryAfter = 30 * time.Second
)

// errModelCatalogUnavailable 标记目录拉取失败，区别于「已取到但回答不可用」（如未授权）。
var errModelCatalogUnavailable = errors.New("获取平台模型目录失败")

type modelCatalog struct {
	// models 只收文本能力条目，供聊天／判定／预算／识图查找。
	// 生图条目单独放在 imageGeneration（顺序在 imageOrder），避免纯生图混进文本选择器。
	models          map[string]PassportModel
	imageGeneration map[string]PassportModel
	imageOrder      []string
	// visionDefaults 来自 Bridge 原始列表、ID 去重之前：两条都标 vision_default
	// 是要上报的配置错误，不能被 map 默默压成一条。
	visionDefaults []PassportModel
	at             time.Time
}

type modelCatalogFailure struct {
	err error
	at  time.Time
}

func modelCatalogKey(scope modelReference, actor string) string {
	scope.Name = ""
	return fmt.Sprintf("%q/%q/%q", scope.Bridge, scope.TenantID, actor)
}

func modelCatalogFromModels(models []PassportModel) modelCatalog {
	next := modelCatalog{models: make(map[string]PassportModel, len(models)), imageGeneration: map[string]PassportModel{}, at: time.Now()}
	for _, m := range models {
		if isImageGenerationModel(m) {
			if _, seen := next.imageGeneration[m.ID]; !seen {
				next.imageOrder = append(next.imageOrder, m.ID)
			}
			next.imageGeneration[m.ID] = m
		}
		if !isChatModel(m) {
			continue
		}
		next.models[m.ID] = m
		if m.VisionDefault {
			next.visionDefaults = append(next.visionDefaults, m)
		}
	}
	return next
}

// 目录是不可变快照。按发起请求时的身份发布；迟到响应绝不能挂到「此刻登录」的账号上。
func (a *App) rememberModelCatalog(scope modelReference, actor string, models []PassportModel) {
	next := modelCatalogFromModels(models)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.modelCatalogs == nil || len(a.modelCatalogs) >= 64 {
		a.modelCatalogs = map[string]modelCatalog{}
	}
	key := modelCatalogKey(scope, actor)
	a.modelCatalogs[key] = next
	delete(a.modelCatalogFailures, key)
}

func (a *App) platformModelCatalog(ctx context.Context, scope modelReference, actor string) (modelCatalog, error) {
	if err := ctx.Err(); err != nil {
		return modelCatalog{}, err
	}
	if !a.tokens.LoggedIn() {
		return modelCatalog{}, errors.New("加载平台模型目录需要登录通行证")
	}
	if a.visionAccountIdentity() != actor {
		return modelCatalog{}, errors.New("加载模型目录期间账号已变化，请重试")
	}
	if scope != platformModelRef(scope.TenantID, "") {
		return modelCatalog{}, errors.New("会话所属平台已变化，请重新打开会话")
	}
	key := modelCatalogKey(scope, actor)
	a.mu.Lock()
	entry, ok := a.modelCatalogs[key]
	a.mu.Unlock()
	if ok && time.Since(entry.at) < modelCatalogTTL {
		return entry, nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if _, err := a.passportModelsContext(lookupCtx, scope.TenantID); err != nil {
		err = fmt.Errorf("%w: %w", errModelCatalogUnavailable, err)
		// A caller that gave up is not evidence that the Bridge is down.
		if ctx.Err() == nil {
			a.mu.Lock()
			if a.modelCatalogFailures == nil || len(a.modelCatalogFailures) >= 64 {
				a.modelCatalogFailures = map[string]modelCatalogFailure{}
			}
			a.modelCatalogFailures[key] = modelCatalogFailure{err: err, at: time.Now()}
			a.mu.Unlock()
		}
		return modelCatalog{}, err
	}
	if err := lookupCtx.Err(); err != nil {
		return modelCatalog{}, err
	}
	if a.visionAccountIdentity() != actor {
		return modelCatalog{}, errors.New("加载模型目录期间账号已变化，请重试")
	}
	a.mu.Lock()
	entry, ok = a.modelCatalogs[key]
	a.mu.Unlock()
	if !ok {
		return modelCatalog{}, errors.New("平台模型目录已变化，请重试")
	}
	return entry, nil
}

// staleModelCatalog returns the last snapshot fetched for this exact scope and
// account, even past its TTL. Only session builds use it: settings and vision
// keep failing explicitly when a refresh fails.
func (a *App) staleModelCatalog(scope modelReference, actor string) (modelCatalog, bool) {
	if a.visionAccountIdentity() != actor || scope != platformModelRef(scope.TenantID, "") {
		return modelCatalog{}, false
	}
	a.mu.Lock()
	entry, ok := a.modelCatalogs[modelCatalogKey(scope, actor)]
	a.mu.Unlock()
	if !ok || time.Since(entry.at) >= modelCatalogStaleLimit {
		return modelCatalog{}, false
	}
	return entry, true
}

// buildModelCatalog is the catalog lookup for session builds. A fetch that just
// failed is not retried for modelCatalogRetryAfter: the build answers from this
// account's stale snapshot, or reports the outage, without another timeout.
// Settings and vision keep using platformModelCatalog, so a manual refresh
// there always reaches the Bridge.
func (a *App) buildModelCatalog(ctx context.Context, scope modelReference, actor string) (modelCatalog, error) {
	a.mu.Lock()
	failure, failed := a.modelCatalogFailures[modelCatalogKey(scope, actor)]
	a.mu.Unlock()
	var err error
	if failed && time.Since(failure.at) < modelCatalogRetryAfter {
		err = failure.err
	} else {
		var catalog modelCatalog
		if catalog, err = a.platformModelCatalog(ctx, scope, actor); !errors.Is(err, errModelCatalogUnavailable) {
			return catalog, err
		}
	}
	if stale, ok := a.staleModelCatalog(scope, actor); ok {
		return stale, nil
	}
	return modelCatalog{}, err
}
