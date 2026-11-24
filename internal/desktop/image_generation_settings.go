package desktop

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/wt68/runcode/internal/imagegentool"
	appwire "github.com/wt68/runcode/internal/protocol"
)

// 默认生图模型与尺寸：按「账号指纹 + Bridge + 租户」分区保存在 desktop.json 里，
// 不保存任何凭据。显式保存；保存时复核目录与权限，读取不发网络请求。

// imageGenerationPreference 是某一作用域已保存的默认。SupportedSizes 在保存时
// 已对照目录档案校验，回合可据此约束模型的 size 而不再拉目录；提交前仍对活目录复核。
type imageGenerationPreference struct {
	Revision       int64           `json:"revision"`
	DefaultModel   *modelReference `json:"defaultModel,omitempty"`
	DefaultSize    string          `json:"defaultSize,omitempty"`
	SupportedSizes []string        `json:"supportedSizes,omitempty"`
	ResponseFormat string          `json:"responseFormat,omitempty"`
}

// preferredResponseFormat 优先 url（响应更小）；档案只提供 b64_json 时再回落（设计 6.2）。
func preferredResponseFormat(formats []string) string {
	if slices.Contains(formats, "url") {
		return "url"
	}
	if slices.Contains(formats, "b64_json") {
		return "b64_json"
	}
	return ""
}

func (p imageGenerationPreference) view(scope string) appwire.ImageGenerationSettings {
	out := appwire.ImageGenerationSettings{ScopeID: scope, Revision: p.Revision, DefaultSize: p.DefaultSize}
	if p.DefaultModel != nil {
		ref := *p.DefaultModel
		out.DefaultModel = &ref
	}
	return out
}

func (p imageGenerationPreference) sameTarget(model *modelReference, size string) bool {
	if (p.DefaultModel == nil) != (model == nil) {
		return false
	}
	return model == nil || (*p.DefaultModel == *model && p.DefaultSize == size)
}

func cloneImageGenerationPreferences(src map[string]imageGenerationPreference) map[string]imageGenerationPreference {
	out := make(map[string]imageGenerationPreference, len(src))
	for scope, pref := range src {
		out[scope] = pref
	}
	return out
}

func (a *App) imageGenerationPreference(scope string) imageGenerationPreference {
	a.imageGenerationMu.RLock()
	defer a.imageGenerationMu.RUnlock()
	return a.imageGenerationPrefs[scope]
}

func (a *App) rememberImageGenerationPreference(scope string, pref imageGenerationPreference) {
	a.imageGenerationMu.Lock()
	if a.imageGenerationPrefs == nil {
		a.imageGenerationPrefs = map[string]imageGenerationPreference{}
	}
	a.imageGenerationPrefs[scope] = pref
	a.imageGenerationMu.Unlock()
}

// GetImageGenerationSettings 返回当前账号在 tenantID 下已保存的默认；从不访问 Bridge。
func (a *App) GetImageGenerationSettings(tenantID string) (appwire.ImageGenerationSettings, error) {
	tenantID = strings.TrimSpace(tenantID)
	account, _, err := a.imageGenerationAccount()
	if err != nil {
		return appwire.ImageGenerationSettings{}, wireError(err)
	}
	scope := imageGenerationScope(account, imageGenerationBridge(), tenantID)
	return a.imageGenerationPreference(scope).view(scope), nil
}

// SaveImageGenerationSettings 对照活目录校验后替换作用域默认。
// 重复提交已确认目标原样返回；目标变了但修订号过期则拒绝。
func (a *App) SaveImageGenerationSettings(req appwire.SaveImageGenerationSettingsRequest) (appwire.ImageGenerationSettings, error) {
	tenantID := strings.TrimSpace(req.TenantID)
	account, epoch, err := a.imageGenerationAccount()
	if err != nil {
		return appwire.ImageGenerationSettings{}, wireError(err)
	}
	bridge := imageGenerationBridge()
	scope := imageGenerationScope(account, bridge, tenantID)
	if req.ScopeID != scope {
		return appwire.ImageGenerationSettings{}, wireError(errors.New("账号或租户已变化，请刷新图片生成设置后重试"))
	}
	var model *modelReference
	size := strings.TrimSpace(req.DefaultSize)
	if req.DefaultModel != nil {
		ref := *req.DefaultModel
		ref.Name = strings.TrimSpace(ref.Name)
		if ref.Name == "" || ref != platformModelRef(tenantID, ref.Name) {
			return appwire.ImageGenerationSettings{}, wireError(errors.New("无效的生图模型引用，请从当前租户的生图模型中选择"))
		}
		model = &ref
	} else {
		size = ""
	}
	current := a.imageGenerationPreference(scope)
	if current.sameTarget(model, size) {
		return current.view(scope), nil
	}
	if current.Revision != req.ExpectedRevision {
		return appwire.ImageGenerationSettings{}, wireError(errors.New("图片生成设置已变化，请刷新后重试"))
	}
	var supported []string
	format := ""
	if model != nil {
		actor := a.visionAccountIdentity()
		ctx, cancel := context.WithTimeout(context.Background(), imageGenerationCatalogTimeout)
		catalog, err := a.freshImageGenerationCatalog(ctx, tenantID, actor)
		cancel()
		if err != nil {
			return appwire.ImageGenerationSettings{}, wireError(err)
		}
		entry, ok := catalog.imageGeneration[model.Name]
		if !ok {
			return appwire.ImageGenerationSettings{}, wireError(errors.New("该生图模型不在当前租户的可用目录中"))
		}
		profile := normalizeImageGenerationProfile(entry.ImageGeneration)
		if !profile.Available {
			return appwire.ImageGenerationSettings{}, wireError(errors.New("该生图模型当前不可用：" + profile.UnavailableReason))
		}
		// 显式尺寸必须落在 Bridge 目录声明的支持列表里；错误值拒绝，不静默改写。
		if size != "" {
			_, _, canonical, ok := imagegentool.ParseSize(size)
			if !ok || !slices.Contains(profile.SupportedSizes, canonical) {
				return appwire.ImageGenerationSettings{}, wireError(errors.New("该生图模型不支持所选尺寸"))
			}
			size = canonical
		}
		// 仅一种尺寸时没有「不固定」：空值钉成该尺寸。
		if size == "" && len(profile.SupportedSizes) == 1 {
			size = profile.SupportedSizes[0]
		}
		supported, format = profile.SupportedSizes, preferredResponseFormat(profile.ResponseFormats)
		if current.sameTarget(model, size) {
			return current.view(scope), nil
		}
	}
	if again, againEpoch, err := a.imageGenerationAccount(); err != nil || again != account || againEpoch != epoch {
		return appwire.ImageGenerationSettings{}, wireError(errors.New("保存期间账号已变化，请重试"))
	}
	var saved imageGenerationPreference
	err = updateRawConfig(func(cfg *desktopConfig) error {
		prev := cfg.ImageGenerationPreferences[scope]
		if prev.Revision != req.ExpectedRevision {
			return errors.New("图片生成设置已变化，请刷新后重试")
		}
		saved = imageGenerationPreference{Revision: prev.Revision + 1, DefaultModel: model, DefaultSize: size, SupportedSizes: supported, ResponseFormat: format}
		if cfg.ImageGenerationPreferences == nil {
			cfg.ImageGenerationPreferences = map[string]imageGenerationPreference{}
		}
		cfg.ImageGenerationPreferences[scope] = saved
		return nil
	})
	if err != nil {
		return appwire.ImageGenerationSettings{}, wireError(err)
	}
	a.rememberImageGenerationPreference(scope, saved)
	return saved.view(scope), nil
}
