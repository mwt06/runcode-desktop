package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/recorder"
)

func TestRecordingArchiveName(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 9, 1, 14, 30, 0, 0, time.Local)
	cases := []struct {
		name  string
		title string
		want  string
	}{
		{"日期在前，与纪要文件名同序", "项目周会", "录音-20260901-项目周会"},
		// 标题是用户随手填的，路径分隔符与 Windows 保留字符必须洗掉，否则这个名字
		// 会被当成一层目录（或者根本创建不了）。
		{"洗掉非法字符", `产品/需求 评审:第一轮`, "录音-20260901-产品_需求 评审_第一轮"},
		{"空标题退回默认名", "   ", "录音-20260901-录音纪要"},
		// 截断正好切在点或空格上时，Windows 不接受这样的目录名。
		{"截断后不留结尾的点与空格", strings.Repeat("会", 39) + " . ", "录音-20260901-" + strings.Repeat("会", 39)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := recordingArchiveName(recorder.SessionMeta{Title: tc.title, StartedAt: started})
			if got != tc.want {
				t.Fatalf("archive name = %q, want %q", got, tc.want)
			}
		})
	}

	long := recordingArchiveName(recorder.SessionMeta{Title: strings.Repeat("会", 80), StartedAt: started})
	if runes := []rune(strings.TrimPrefix(long, "录音-20260901-")); len(runes) != maxArchiveTitleRunes {
		t.Fatalf("长标题截到 %d 个字符，want %d（按 rune 截，不能把一个汉字劈成两半）", len(runes), maxArchiveTitleRunes)
	}
}

// seedRecording 造一场录音的落盘目录：两条 WAV、一份转写、一个 meta。
func seedRecording(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"mic.wav":       strings.Repeat("A", 4096),
		"sys.wav":       strings.Repeat("B", 4096),
		"transcript.md": "**[00:03] S1**：开始吧。",
		"meta.json":     `{"id":"rec_1"}`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestArchiveRecordingCopiesWholeSessionIntoWorkspace(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	src := filepath.Join(t.TempDir(), "rec_1")
	seedRecording(t, src)

	app := &App{workspace: ws}
	meta := recorder.SessionMeta{ID: "rec_1", Title: "项目周会", StartedAt: time.Date(2026, 9, 1, 9, 0, 0, 0, time.Local)}
	dest, err := app.archiveRecording(src, meta)
	if err != nil {
		t.Fatalf("archiveRecording: %v", err)
	}
	if want := filepath.Join(ws, "录音-20260901-项目周会"); dest != want {
		t.Fatalf("dest = %q, want %q", dest, want)
	}
	for _, name := range []string{"mic.wav", "sys.wav", "transcript.md", "meta.json"} {
		if _, err := os.Stat(filepath.Join(dest, name)); err != nil {
			t.Errorf("工作区那份缺了 %s: %v", name, err)
		}
	}
	// 是复制不是移动：应用数据目录那份是补转写唯一的依据，不能被搬走。
	if _, err := os.Stat(filepath.Join(src, "mic.wav")); err != nil {
		t.Fatalf("原件被动了: %v", err)
	}
	// 中转用的临时目录不能留在工作区里。
	entries, err := os.ReadDir(ws)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".录音-") {
			t.Fatalf("工作区里留下了中转目录 %s", e.Name())
		}
	}
}

func TestArchiveRecordingWithoutWorkspaceIsNotAFailure(t *testing.T) {
	t.Parallel()

	src := filepath.Join(t.TempDir(), "rec_1")
	seedRecording(t, src)

	app := &App{}
	dest, err := app.archiveRecording(src, recorder.SessionMeta{ID: "rec_1", Title: "无工作区"})
	if err != nil {
		t.Fatalf("没有工作区时不该报错，得到 %v", err)
	}
	if dest != "" {
		t.Fatalf("dest = %q, want 空（没地方可放）", dest)
	}
}

// 同一天开两场同名的会不能互相覆盖——第二场缀上录音 id。
func TestArchiveRecordingDoesNotOverwriteASameDayNamesake(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	started := time.Date(2026, 9, 1, 9, 0, 0, 0, time.Local)

	first := filepath.Join(t.TempDir(), "rec_1")
	seedRecording(t, first)
	app := &App{workspace: ws}
	if _, err := app.archiveRecording(first, recorder.SessionMeta{ID: "rec_1", Title: "周会", StartedAt: started}); err != nil {
		t.Fatalf("first archive: %v", err)
	}

	second := filepath.Join(t.TempDir(), "rec_2")
	seedRecording(t, second)
	if err := os.WriteFile(filepath.Join(second, "transcript.md"), []byte("第二场"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest, err := app.archiveRecording(second, recorder.SessionMeta{ID: "rec_2", Title: "周会", StartedAt: started})
	if err != nil {
		t.Fatalf("second archive: %v", err)
	}
	if want := filepath.Join(ws, "录音-20260901-周会-rec_2"); dest != want {
		t.Fatalf("second dest = %q, want %q", dest, want)
	}
	body, err := os.ReadFile(filepath.Join(ws, "录音-20260901-周会", "transcript.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) == "第二场" {
		t.Fatal("第二场把第一场覆盖了")
	}
}
