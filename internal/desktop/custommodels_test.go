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

	// 重新 List：APIKey 应解密可读（Windows DPAPI 往返）
	got := app.ListCustomModels()
	if len(got) != 1 || got[0].APIKey != "sk-local" || got[0].APIKeyProtected != "" {
		t.Fatalf("list = %+v, want decrypted key", got)
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
