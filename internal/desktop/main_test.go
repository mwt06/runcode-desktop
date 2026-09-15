package desktop

import (
	"os"
	"testing"
)

// TestMain 关掉自动更新检查。
//
// 它是 newWithBuild 起的一个 goroutine：睡 8 秒再去查更新，途中会在 UserCacheDir
// 下建目录。测试里那个目录是 t.TempDir() 隔离出来的，等 goroutine 醒来时早被清理，
// 它又把目录建回去——于是**别的**测试的 TempDir 清理报 "directory is not empty"。
//
// 这个竞态很难查：失败的总是无辜的那条测试（谁的清理正好撞上就是谁），而且只在
// 整包跑、机器够慢时才出现。关掉它，测试就不再依赖"8 秒内跑完"这种隐含约定。
// 诊断日志同理，而且它才是这个竞态的**主要**来源：debugLog 每次调用都重新算一遍
// os.UserConfigDir()，所以任何活过自己那条测试的 goroutine 只要再记一行日志，就会
// 按当时的 APPDATA 把 <TempDir>/runcode/desktop.log 建出来——而那时目录正属于别人。
// 关掉自动更新只堵了其中一条路；关掉日志才是把这一类堵死。
func TestMain(m *testing.M) {
	autoUpdateEnabled = false
	debugLogEnabled = false
	os.Exit(m.Run())
}
