package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// attemptFixture 把缓存目录和「当前版本」都换成测试自己的，测完还原。
func attemptFixture(t *testing.T, current string) string {
	t.Helper()
	dir := t.TempDir()
	prevDir, prevVersion := updateCacheDir, appVersion
	updateCacheDir = func() (string, error) { return dir, nil }
	appVersion = current
	t.Cleanup(func() {
		updateCacheDir = prevDir
		appVersion = prevVersion
	})
	return dir
}

// TestInstallAttemptSilentWhenItWorked 装成了就什么都不说。
//
// 更新顺利是常态，为它多弹一句话就是给每一次成功的更新加一条噪音。
func TestInstallAttemptSilentWhenItWorked(t *testing.T) {
	attemptFixture(t, "0.1.4")
	if err := writeInstallAttempt("0.1.4"); err != nil {
		t.Fatal(err)
	}
	if note := installAttemptNote(); note != "" {
		t.Errorf("装成了却报了一句：%q", note)
	}
}

// TestInstallAttemptReportsWhenItDidNot 没装上就必须说出来。
//
// 这是这条链路上最大的一个窟窿：在此之前，用户取消 UAC、杀软拦下安装器、安装器
// 因为文件被占用放弃，表现全都一样——应用关掉又回来了、版本没变、没有任何人说过
// 为什么。用户唯一能得到的信息是「更新提示还在」，而他已经点过一次更新了。
func TestInstallAttemptReportsWhenItDidNot(t *testing.T) {
	attemptFixture(t, "0.1.3")
	if err := writeInstallAttempt("0.1.4"); err != nil {
		t.Fatal(err)
	}
	note := installAttemptNote()
	if note == "" {
		t.Fatal("没装上却一声不吭")
	}
	// 两个版本号都得出现：只说"更新失败"的话，用户既不知道失败的是哪一版，
	// 也不知道自己现在是哪一版。
	for _, want := range []string{"0.1.4", "0.1.3"} {
		if !strings.Contains(note, want) {
			t.Errorf("提示里没有 %s：%q", want, note)
		}
	}
}

// TestInstallAttemptSilentWhenAlreadyNewer 手工装了个更新的版本之后不该再提。
//
// 判据是「当前版本 < 目标版本」，不是「不相等」：用户完全可能在这中间自己下了个
// 更新的包装上，那时上一次自动更新没走完已经不再是个问题。
func TestInstallAttemptSilentWhenAlreadyNewer(t *testing.T) {
	attemptFixture(t, "0.2.0")
	if err := writeInstallAttempt("0.1.4"); err != nil {
		t.Fatal(err)
	}
	if note := installAttemptNote(); note != "" {
		t.Errorf("已经更新了却还在报上一次：%q", note)
	}
}

// TestInstallAttemptSpeaksOnlyOnce 读一次之后记录就该没了。
//
// 留着的话，一个装不上的版本会在此后**每次**启动都弹同一句警告，而用户对此无能
// 为力——那不是提示，是噪音。
func TestInstallAttemptSpeaksOnlyOnce(t *testing.T) {
	dir := attemptFixture(t, "0.1.3")
	if err := writeInstallAttempt("0.1.4"); err != nil {
		t.Fatal(err)
	}
	if note := installAttemptNote(); note == "" {
		t.Fatal("第一次就没说话")
	}
	if note := installAttemptNote(); note != "" {
		t.Errorf("第二次还在说：%q", note)
	}
	if _, err := os.Stat(filepath.Join(dir, "install-attempt.json")); !os.IsNotExist(err) {
		t.Errorf("记录文件没删掉: %v", err)
	}
}

// TestInstallAttemptSurvivesGarbage 记录被写坏时当作「没有记录」，不能崩。
//
// 它是磁盘上的文件，可能被断电写了一半、被清理工具截断。为一个诊断用的记录让应用
// 起不来，是本末倒置。
func TestInstallAttemptSurvivesGarbage(t *testing.T) {
	dir := attemptFixture(t, "0.1.3")
	if err := os.WriteFile(filepath.Join(dir, "install-attempt.json"), []byte("{半个"), 0o600); err != nil {
		t.Fatal(err)
	}
	if note := installAttemptNote(); note != "" {
		t.Errorf("坏记录不该产生提示：%q", note)
	}
}
