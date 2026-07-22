package desktop

import (
	"errors"
	"strings"
)

// ListCustomModels 返回解密后的自定义模型列表（给表单编辑/起会话用）。
func (a *App) ListCustomModels() []CustomModel {
	raw := loadRawConfig().CustomModels
	out := make([]CustomModel, 0, len(raw))
	for _, m := range raw {
		out = append(out, unprotectCustomModel(m))
	}
	return out
}

// findCustomModel 按显示名返回解密后的自定义模型，供对话内模型选择器切换时取
// 直连参数（baseURL / apiKey / model）。名称是 Save/Delete 的唯一键。
func (a *App) findCustomModel(name string) (CustomModel, bool) {
	name = strings.TrimSpace(name)
	for _, m := range a.ListCustomModels() {
		if m.Name == name {
			return m, true
		}
	}
	return CustomModel{}, false
}

// SaveCustomModel 新增或同名覆盖一个自定义模型，返回最新列表（已解密）。
func (a *App) SaveCustomModel(m CustomModel) ([]CustomModel, error) {
	m.Name = strings.TrimSpace(m.Name)
	m.Model = strings.TrimSpace(m.Model)
	if m.Name == "" {
		return nil, wireError(errors.New("模型名称不能为空"))
	}
	if m.Model == "" {
		return nil, wireError(errors.New("模型 ID 不能为空"))
	}
	updateRawConfig(func(cfg *StartSessionRequest) {
		next := make([]CustomModel, 0, len(cfg.CustomModels)+1)
		for _, existing := range cfg.CustomModels {
			if existing.Name != m.Name {
				next = append(next, existing)
			}
		}
		next = append(next, protectCustomModel(m))
		cfg.CustomModels = next
	})
	return a.ListCustomModels(), nil
}

// DeleteCustomModel 按名称删除，返回最新列表。
func (a *App) DeleteCustomModel(name string) []CustomModel {
	name = strings.TrimSpace(name)
	updateRawConfig(func(cfg *StartSessionRequest) {
		next := cfg.CustomModels[:0]
		for _, m := range cfg.CustomModels {
			if m.Name != name {
				next = append(next, m)
			}
		}
		cfg.CustomModels = next
	})
	return a.ListCustomModels()
}

func protectCustomModel(m CustomModel) CustomModel {
	m.APIKeyProtected, _ = protectSecret(m.APIKey)
	m.APIKey = ""
	return m
}

func unprotectCustomModel(m CustomModel) CustomModel {
	if m.APIKeyProtected != "" {
		if s, ok := unprotectSecret(m.APIKeyProtected); ok {
			m.APIKey = s
		}
	}
	m.APIKeyProtected = ""
	return m
}
