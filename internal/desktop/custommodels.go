package desktop

import (
	"errors"
	"fmt"
	"strings"

	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
)

// supportedCustomModelProvider gates a saved profile's provider against the
// engine's own registry instead of a list copied here: desktop passes the value
// straight through to engine.Config.Provider, so the registry is the only
// authority on what will actually build. A provider added engine-side becomes
// usable without a second list to update.
func supportedCustomModelProvider(provider string) bool {
	return llm.IsRegistered(provider)
}

// unsupportedProviderError names what is actually available rather than leaving
// the user to guess the spelling.
func unsupportedProviderError(provider string) error {
	return fmt.Errorf("不支持的服务商 %q（可选：%s）", provider, strings.Join(llm.Registered(), "、"))
}

// ListCustomModels 返回脱敏后的自定义模型列表。连接密钥只在后端解析会话时
// 解密，前端仅知道是否已配置，不能读取明文或落盘密文。
func (a *App) ListCustomModels() []CustomModel {
	return publicCustomModels(loadRawConfig().CustomModels)
}

func publicCustomModels(raw []CustomModel) []CustomModel {
	out := make([]CustomModel, 0, len(raw))
	for _, m := range raw {
		m.Provider = normalizeCustomModelProvider(m.Provider)
		m.HasAPIKey = m.APIKeyProtected != ""
		m.APIKey = ""
		m.APIKeyProtected = ""
		out = append(out, m)
	}
	return out
}

// resolveCustomModel 按显示名解析一条直连配置。密钥只在这里短暂进入 Go 内存，
// 供起会话和对话内切换使用，不经过 Wails bridge 返回前端。
func (a *App) resolveCustomModel(name string) (CustomModel, error) {
	name = strings.TrimSpace(name)
	for _, m := range loadRawConfig().CustomModels {
		if m.Name != name {
			continue
		}
		m.Provider = normalizeCustomModelProvider(m.Provider)
		if !supportedCustomModelProvider(m.Provider) {
			return CustomModel{}, fmt.Errorf("自定义模型 %q：%w", name, unsupportedProviderError(m.Provider))
		}
		if m.APIKeyProtected != "" {
			key, ok := unprotectSecret(m.APIKeyProtected)
			if !ok {
				return CustomModel{}, fmt.Errorf("自定义模型 %q 的 API 密钥无法解密，请重新保存密钥", name)
			}
			m.APIKey = key
		}
		m.HasAPIKey = m.APIKey != ""
		m.APIKeyProtected = ""
		return m, nil
	}
	return CustomModel{}, fmt.Errorf("自定义模型不存在: %s", name)
}

// resolveCustomModelRequest expands a saved profile into a session request entirely
// in the backend. CustomModelName remains set so buildConfig knows empty fields are
// explicit and must not inherit unrelated ANTHROPIC_* environment credentials.
func (a *App) resolveCustomModelRequest(req StartSessionRequest) (StartSessionRequest, error) {
	name := strings.TrimSpace(req.CustomModelName)
	if name == "" {
		return req, nil
	}
	cm, err := a.resolveCustomModel(name)
	if err != nil {
		return StartSessionRequest{}, err
	}
	req.CustomModelName = name
	req.Provider = cm.Provider
	req.Model = cm.Model
	req.BaseURL = cm.BaseURL
	req.APIKey = cm.APIKey
	req.AuthToken = ""
	return req, nil
}

// customModelPersistenceRequest reduces a resolved custom connection back to its
// safe profile reference before writing desktop.json. It also ignores any expanded
// connection fields an old or untrusted client sent alongside CustomModelName.
func customModelPersistenceRequest(original, resolved StartSessionRequest) StartSessionRequest {
	if strings.TrimSpace(original.CustomModelName) == "" {
		return original
	}
	original.CustomModelName = resolved.CustomModelName
	original.Provider = resolved.Provider
	original.Model = resolved.Model
	original.BaseURL = ""
	original.APIKey = ""
	original.AuthToken = ""
	original.APIKeyProtected = ""
	original.AuthTokenProtected = ""
	return original
}

// SaveCustomModel 新增或修改一个自定义模型，返回最新的脱敏列表。编辑时
// OriginalName 定位原记录；APIKey 留空保留旧密钥，只有 ClearAPIKey 才清除。
func (a *App) SaveCustomModel(req SaveCustomModelRequest) ([]CustomModel, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.OriginalName = strings.TrimSpace(req.OriginalName)
	req.Provider = normalizeCustomModelProvider(req.Provider)
	req.Model = strings.TrimSpace(req.Model)
	req.BaseURL = strings.TrimSpace(req.BaseURL)
	if req.Name == "" {
		return nil, wireError(errors.New("模型名称不能为空"))
	}
	if req.Model == "" {
		return nil, wireError(errors.New("模型 ID 不能为空"))
	}
	if !supportedCustomModelProvider(req.Provider) {
		return nil, wireError(unsupportedProviderError(req.Provider))
	}
	if req.ClearAPIKey && req.APIKey != "" {
		return nil, wireError(errors.New("不能同时替换和清除 API 密钥"))
	}

	var result []CustomModel
	err := updateRawConfig(func(cfg *StartSessionRequest) error {
		source := -1
		target := -1
		for i, existing := range cfg.CustomModels {
			if existing.Name == req.OriginalName {
				source = i
			}
			if existing.Name == req.Name {
				target = i
			}
		}

		if req.OriginalName != "" {
			if source < 0 {
				return fmt.Errorf("原自定义模型不存在: %s", req.OriginalName)
			}
			if req.Name != req.OriginalName && target >= 0 && target != source {
				return fmt.Errorf("自定义模型名称已存在: %s", req.Name)
			}
		} else {
			// 保留旧调用方“同名保存即更新”的幂等语义；设置页编辑始终携带
			// OriginalName，因此重命名绝不会静默覆盖另一条配置。
			source = target
		}
		if req.ClearAPIKey && source < 0 {
			return errors.New("新建模型时不能清除尚不存在的 API 密钥")
		}

		persisted := CustomModel{
			Name:     req.Name,
			Provider: req.Provider,
			Model:    req.Model,
			BaseURL:  req.BaseURL,
		}
		switch {
		case req.ClearAPIKey:
			// Explicit clear: leave both secret fields empty.
		case req.APIKey != "":
			protected, ok := protectSecret(req.APIKey)
			if !ok {
				return errors.New("当前系统无法安全保存 API 密钥，请使用无需密钥的端点或配置系统凭据存储")
			}
			persisted.APIKeyProtected = protected
		case source >= 0:
			// Preserve the opaque ciphertext without decrypting/re-encrypting it.
			persisted.APIKeyProtected = cfg.CustomModels[source].APIKeyProtected
		}

		if source >= 0 {
			cfg.CustomModels[source] = persisted
		} else {
			cfg.CustomModels = append(cfg.CustomModels, persisted)
		}
		if req.OriginalName != "" && req.OriginalName != req.Name && cfg.CustomModelName == req.OriginalName {
			cfg.CustomModelName = req.Name
		}
		result = publicCustomModels(cfg.CustomModels)
		return nil
	})
	if err != nil {
		return nil, wireError(err)
	}
	return result, nil
}

// normalizeCustomModelProvider canonicalizes the stored provider id. The desktop
// form offers openai（Chat Completions）、openai-responses（Responses API）和
// anthropic；whatever it stores must match an engine registry name verbatim.
func normalizeCustomModelProvider(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return "openai" // compatibility with desktop.json written before Provider existed
	}
	return provider
}

// DeleteCustomModel 按名称删除，返回最新列表。未知名称是幂等 no-op。
func (a *App) DeleteCustomModel(name string) ([]CustomModel, error) {
	name = strings.TrimSpace(name)
	var result []CustomModel
	if err := updateRawConfig(func(cfg *StartSessionRequest) error {
		next := cfg.CustomModels[:0]
		for _, m := range cfg.CustomModels {
			if m.Name != name {
				next = append(next, m)
			}
		}
		cfg.CustomModels = next
		if cfg.CustomModelName == name {
			cfg.CustomModelName = ""
			cfg.Provider = ""
			cfg.Model = ""
			cfg.BaseURL = ""
			cfg.APIKey = ""
			cfg.AuthToken = ""
			cfg.APIKeyProtected = ""
			cfg.AuthTokenProtected = ""
		}
		result = publicCustomModels(cfg.CustomModels)
		return nil
	}); err != nil {
		return nil, wireError(err)
	}
	return result, nil
}
