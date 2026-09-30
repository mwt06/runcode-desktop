package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	appwire "github.com/wt68/runcode/internal/protocol"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
)

type modelReference = appwire.ModelReference

func platformModelRef(tenant, model string) modelReference {
	return modelReference{Kind: "platform", Name: strings.TrimSpace(model), Bridge: strings.TrimRight(passportConfig().BridgeBaseURL, "/"), TenantID: strings.TrimSpace(tenant)}
}

// GetVisionSettings returns credential-free image routing preferences.
func (a *App) GetVisionSettings() appwire.VisionSettings {
	v := loadRawConfig().Vision
	v.Bridge = strings.TrimRight(passportConfig().BridgeBaseURL, "/")
	if v.PlatformCapabilities == nil {
		v.PlatformCapabilities = []appwire.PlatformImageCapability{}
	}
	return v
}

// SaveVisionSettings validates the fixed destination without changing the main model.
func (a *App) SaveVisionSettings(req appwire.SaveVisionSettingsRequest) (appwire.VisionSettings, error) {
	if req.Disabled && req.DefaultModel != nil {
		return appwire.VisionSettings{}, wireError(errors.New("关闭识图兜底时不能同时指定识图模型"))
	}
	revision := loadRawConfig().VisionRevision
	if req.DefaultModel != nil {
		ref := *req.DefaultModel
		ref.Name = strings.TrimSpace(ref.Name)
		if _, err := a.resolveVisionModel(ref); err != nil {
			return appwire.VisionSettings{}, wireError(err)
		}
		req.DefaultModel = &ref
	}
	err := updateRawConfig(func(cfg *desktopConfig) error {
		if cfg.VisionRevision != revision {
			return errors.New("模型设置已变化，请重新选择识图模型")
		}
		// Recheck custom state under the write lock; an edit must not invalidate the
		// destination between validation and persistence.
		if req.DefaultModel != nil && req.DefaultModel.Kind == "custom" {
			found := false
			for _, m := range cfg.CustomModels {
				if m.Name == req.DefaultModel.Name {
					found = m.SupportsImages != nil && *m.SupportsImages
					break
				}
			}
			if !found {
				return errors.New("请选择已标记支持图片的自定义模型")
			}
		}
		cfg.Vision.DefaultModel = req.DefaultModel
		cfg.Vision.Disabled = req.Disabled
		cfg.VisionRevision++
		return nil
	})
	return a.GetVisionSettings(), wireError(err)
}

// SetPlatformImageCapability sets or removes one tenant-scoped local override.
func (a *App) SetPlatformImageCapability(req appwire.SetPlatformImageCapabilityRequest) (appwire.VisionSettings, error) {
	expected := platformModelRef(req.Model.TenantID, req.Model.Name)
	if req.Model != expected || expected.Name == "" {
		return appwire.VisionSettings{}, wireError(errors.New("无效的平台模型引用"))
	}
	models, err := a.PassportModels(expected.TenantID)
	if err != nil {
		return appwire.VisionSettings{}, err
	}
	found := false
	for _, m := range models {
		if m.ID == expected.Name {
			found = true
			break
		}
	}
	if !found {
		return appwire.VisionSettings{}, wireError(errors.New("该租户下没有此模型"))
	}
	err = updateRawConfig(func(cfg *desktopConfig) error {
		if cfg.Vision.DefaultModel != nil && *cfg.Vision.DefaultModel == expected && (req.SupportsImages == nil || !*req.SupportsImages) {
			return errors.New("请先更换或清除默认识图模型，再修改它的能力标记")
		}
		next := make([]appwire.PlatformImageCapability, 0, len(cfg.Vision.PlatformCapabilities)+1)
		for _, m := range cfg.Vision.PlatformCapabilities {
			if m.Model != expected {
				next = append(next, m)
			}
		}
		if req.SupportsImages != nil {
			next = append(next, appwire.PlatformImageCapability{Model: expected, SupportsImages: *req.SupportsImages})
		}
		cfg.Vision.PlatformCapabilities = next
		cfg.VisionRevision++
		return nil
	})
	return a.GetVisionSettings(), wireError(err)
}

func imageOverride(cfg desktopConfig, ref modelReference) *bool {
	for _, m := range cfg.Vision.PlatformCapabilities {
		if m.Model == ref {
			v := m.SupportsImages
			return &v
		}
	}
	return nil
}

func (a *App) resolveVisionModel(ref modelReference) (engine.Config, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return a.resolveVisionModelFrom(ctx, ref, loadRawConfig())
}

func (a *App) resolveVisionModelFrom(ctx context.Context, ref modelReference, stored desktopConfig) (engine.Config, error) {
	switch ref.Kind {
	case "custom":
		if ref.Bridge != "" || ref.TenantID != "" {
			return engine.Config{}, errors.New("无效的自定义模型引用")
		}
		cm, err := resolveCustomModelFrom(stored, ref.Name)
		if err != nil {
			return engine.Config{}, err
		}
		if cm.SupportsImages == nil || !*cm.SupportsImages {
			return engine.Config{}, errors.New("默认识图模型未标记为支持图片")
		}
		cfg := engine.Config{Provider: cm.Provider, Model: cm.Model, BaseURL: cm.BaseURL, APIKey: cm.APIKey, MaxTokens: 4096, MaxRetries: -1}
		// Codex is translated by a scoped frozen proxy when inference starts.
		return cfg, err
	case "platform":
		if ref != platformModelRef(ref.TenantID, ref.Name) {
			return engine.Config{}, errors.New("识图模型所属平台已变化，请重新选择")
		}
		if !a.tokens.LoggedIn() {
			return engine.Config{}, errNotLoggedIn
		}
		models, err := a.passportModelsContext(ctx, ref.TenantID)
		if err != nil {
			return engine.Config{}, err
		}
		for _, m := range models {
			if m.ID == ref.Name {
				support := a.modelImageSupport(ctx, ref, m.ID, stored)
				if support == nil || !*support {
					return engine.Config{}, errors.New("默认识图模型未标记为支持图片")
				}
				cfg := engine.Config{Provider: "openai", Model: m.ID, BaseURL: ref.Bridge + tenantPathPrefix(ref.TenantID) + "/v1", MaxTokens: 4096, MaxRetries: -1}
				actor := a.visionAccountIdentity()
				cfg.TokenSource = func() (string, error) {
					token, err := a.tokens.Token()
					if err != nil {
						return "", err
					}
					if a.visionAccountIdentity() != actor {
						return "", errors.New("识图期间账号已切换，请重试")
					}
					return token, nil
				}
				cfg.OnUnauthorized = func() {
					if a.visionAccountIdentity() == actor {
						a.tokens.ForceRefresh()
					}
				}
				return cfg, nil
			}
		}
		return engine.Config{}, fmt.Errorf("默认识图模型 %q 已不可用", ref.Name)
	default:
		return engine.Config{}, errors.New("请选择平台模型或自定义模型作为识图兜底")
	}
}

// referenceForBuild is only used for callers intentionally inheriting the
// next-session configuration. Explicit starts/switches/branches pass a reference.
func (a *App) referenceForBuild(cfg engine.Config, passport bool, tenant string, refs []modelReference) modelReference {
	if passport {
		return platformModelRef(tenant, cfg.Model)
	}
	if len(refs) > 0 {
		return refs[0]
	}
	a.mu.Lock()
	ref := a.configModelRef
	a.mu.Unlock()
	return ref
}

func (a *App) visionReference(id string, fallback modelReference) modelReference {
	a.mu.Lock()
	defer a.mu.Unlock()
	if e := a.entryLocked(id); e != nil {
		return e.modelRef
	}
	return fallback
}

func (a *App) renameVisionReferences(from, to string) {
	if from == "" || from == to {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, e := range a.sessions {
		if e.modelRef.Kind == "custom" && e.modelRef.Name == from {
			e.modelRef.Name = to
		}
	}
	if a.configModelRef.Kind == "custom" && a.configModelRef.Name == from {
		a.configModelRef.Name = to
	}
}
