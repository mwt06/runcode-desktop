package desktop

import "testing"

func TestCustomModelsCRUDRoundTrip(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir()) // 隔离 desktop.json（Windows: os.UserConfigDir 读 APPDATA）

	app := New(&recordingSink{})
	if got := app.ListCustomModels(); len(got) != 0 {
		t.Fatalf("initial = %+v, want empty", got)
	}

	list, err := app.SaveCustomModel(CustomModel{Name: "本地 Ollama", Model: "qwen2.5-coder", BaseURL: "http://localhost:11434/v1", APIKey: "sk-local"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "本地 Ollama" {
		t.Fatalf("after save = %+v", list)
	}

	// 重新 List：Windows(DPAPI) 上 APIKey 应解密可读；无平台加密时（secret_other 的
	// no-op）密钥按设计被丢弃——两种情况 protected 字段都必须已清空
	got := app.ListCustomModels()
	if len(got) != 1 || got[0].APIKeyProtected != "" {
		t.Fatalf("list = %+v, want one entry with cleared protected field", got)
	}
	if _, ok := protectSecret("probe"); ok {
		if got[0].APIKey != "sk-local" {
			t.Fatalf("list = %+v, want decrypted key on this platform", got)
		}
	} else if got[0].APIKey != "" {
		t.Fatalf("list = %+v, want dropped key on platform without secret protection", got)
	}

	// 同名覆盖
	list, err = app.SaveCustomModel(CustomModel{Name: "本地 Ollama", Model: "llama3", BaseURL: "http://localhost:11434/v1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Model != "llama3" {
		t.Fatalf("after overwrite = %+v", list)
	}

	if list = app.DeleteCustomModel("本地 Ollama"); len(list) != 0 {
		t.Fatalf("after delete = %+v", list)
	}
}

func TestSaveCustomModelValidates(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	app := New(&recordingSink{})
	if _, err := app.SaveCustomModel(CustomModel{Name: "", Model: "m"}); err == nil {
		t.Fatal("want error for empty name")
	}
	if _, err := app.SaveCustomModel(CustomModel{Name: "n", Model: ""}); err == nil {
		t.Fatal("want error for empty model")
	}
}

func TestFindCustomModel(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	app := New(&recordingSink{})
	if _, err := app.SaveCustomModel(CustomModel{Name: "本地 Ollama", Model: "qwen2.5-coder", BaseURL: "http://localhost:11434/v1"}); err != nil {
		t.Fatal(err)
	}
	got, ok := app.findCustomModel("  本地 Ollama  ") // 名称前后空白应被容错
	if !ok || got.Model != "qwen2.5-coder" || got.BaseURL != "http://localhost:11434/v1" {
		t.Fatalf("findCustomModel = %+v, ok=%v", got, ok)
	}
	if _, ok := app.findCustomModel("不存在"); ok {
		t.Fatal("want not found for unknown name")
	}
}

func TestSwitchModelGuards(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	app := New(&recordingSink{})
	// 空模型名：在取会话前就应报错。
	if _, err := app.SwitchModel("platform", "   "); err == nil {
		t.Fatal("want error for empty model name")
	}
	// 无会话：任何切换都应返回 errNoSession。
	if _, err := app.SwitchModel("platform", "glm-4.6"); err != errNoSession {
		t.Fatalf("SwitchModel without session = %v, want errNoSession", err)
	}
	if _, err := app.SwitchModel("custom", "本地 Ollama"); err != errNoSession {
		t.Fatalf("SwitchModel custom without session = %v, want errNoSession", err)
	}
}
