package desktop

import (
	"testing"
)

// TestWatchArgsSurviveNastyPaths 参数必须原样送到看门进程。
//
// 中文、空格、单引号、结尾的反斜杠四样都真会出现（"C:\Program Files\国家开放大学\
// 智开\智开.exe"，而 Windows 用户名里带单引号也不是没有）。这条守的是换掉 PowerShell
// 那一版的根本理由：那时参数是拼进一段脚本文本的，引错了的后果是看门进程解析失败、
// 静默退出，用户等来的是"应用关掉之后再也没回来"。走 argv 之后转义由 exec 负责，
// 这里验的是我们自己这一层没把它搞坏。
func TestWatchArgsSurviveNastyPaths(t *testing.T) {
	want := watchArgs{
		PID:       4321,
		Exe:       `C:\Program Files\国家开放大学\智开\智开.exe`,
		Expect:    "0.1.4",
		Installer: `C:\Users\o'brien\AppData\Local\runcode\updates\zhikai 0.1.4.exe`,
	}
	argv := append([]string{`C:\app\智开.exe`}, buildWatchArgs(want)...)
	got, err := parseWatchArgs(argv)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got != want {
		t.Errorf("往返之后变了：\n得到 %+v\n期望 %+v", got, want)
	}
}

// TestIsUpdateWatchOnlyMatchesLeadingFlag 开关只认第一个参数。
//
// 这一条比看上去要紧：判断为真就意味着**跳过整个应用启动**。哪怕这个字符串只是
// 碰巧出现在某个后面的参数里（比如将来某个 --note "别用 --update-watch"），也绝不
// 能让用户双击图标之后得到一个什么都不做就退出的进程。
func TestIsUpdateWatchOnlyMatchesLeadingFlag(t *testing.T) {
	if !IsUpdateWatch([]string{"app.exe", updateWatchFlag, "--pid", "1"}) {
		t.Error("第一个参数就是开关，应当认出来")
	}
	for _, args := range [][]string{
		{"app.exe"},
		{"app.exe", "--note", updateWatchFlag},
		{"app.exe", "--pid", "1", updateWatchFlag},
		{},
	} {
		if IsUpdateWatch(args) {
			t.Errorf("%v 不该被当成看门模式", args)
		}
	}
}

// TestParseWatchArgsRejectsIncomplete 少了关键参数就报错，不能带着空值往下跑。
//
// 少了 --exe 的话，看门进程会去"拉起空路径"；少了 --expect 的话，它判断不了装没装
// 好，只会一直等到超时。两种都是白跑一趟，而且没有任何人会看到。宁可当场退出并在
// 日志里留一行。
func TestParseWatchArgsRejectsIncomplete(t *testing.T) {
	for name, args := range map[string][]string{
		"没有 exe":    {"app.exe", updateWatchFlag, "--expect", "0.1.4"},
		"没有 expect": {"app.exe", updateWatchFlag, "--exe", `C:\app\a.exe`},
		"根本不是看门模式":  {"app.exe", "--pid", "1"},
		"参数写错":      {"app.exe", updateWatchFlag, "--nope"},
	} {
		if _, err := parseWatchArgs(args); err == nil {
			t.Errorf("%s：应当报错，实际通过了", name)
		}
	}
}
