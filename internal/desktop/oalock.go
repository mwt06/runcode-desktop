package desktop

// OA 锁:一条会话读过 OA 之后,就永久绑在某个本地模型上。
//
// # 为什么需要一把"锁"而不只是"切一次模型"
//
// 切模型解决的是"OA 数据发给谁";锁解决的是**此后**的每一次请求发给谁。会话历史
// 里一旦有了 OA 数据,后面每一轮都会把整段历史重新发出去——包括自动标题、压缩摘要、
// 以及用户在选择器里手动切回云端模型之后的所有请求。少了这把锁,前面那次切换只是
// 把泄漏推迟了一轮。
//
// # 为什么要落盘
//
// 会话可以被关掉再恢复(Resume)。恢复出来的历史仍然带着 OA 数据,而"这条会话读过
// OA"这件事只存在于上一个进程的内存里。不落盘的表现是:关掉重开,锁就没了,用户
// 可以若无其事地切回云端模型,然后整段 OA 历史被发出去——**而且没有任何迹象**。
//
// 文件放 <ws>/.runcode/oa/<sessionID>.json,与计划(plans/)、编辑记录(edits/)同一
// 套约定。内容只有"锁到哪个本地模型"和"什么时候锁的",够用且可读。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// oaLock 是一条会话的 OA 锁。存在即锁定。
type oaLock struct {
	// LocalModel 是这条会话被绑定到的本地模型 id。
	LocalModel string `json:"localModel"`
	// At 是锁产生的时刻,仅供排查(用户问"它为什么不让我换模型"时,这是唯一的线索)。
	At time.Time `json:"at"`
}

func oaLockPath(workspace, sessionID string) string {
	if strings.TrimSpace(workspace) == "" || strings.TrimSpace(sessionID) == "" {
		return ""
	}
	return filepath.Join(workspace, ".runcode", "oa", sessionID+".json")
}

// readOALock 读回一条会话的锁。文件不存在、读不动、内容坏掉都报"没锁"。
//
// 坏文件报"没锁"而不是"锁着"是有意的:锁的作用是拦住用户的操作,拿一个解析不了的
// 文件去拦人,用户既看不懂也解不开。真正的防线是 oaTools 的闸门——它每次都现场比对
// 当前模型,不依赖这个文件。这里只负责"重启后仍然记得"。
func readOALock(workspace, sessionID string) (oaLock, bool) {
	path := oaLockPath(workspace, sessionID)
	if path == "" {
		return oaLock{}, false
	}
	data, err := os.ReadFile(path) //nolint:gosec // 路径由工作区与会话 id 拼出，非用户输入
	if err != nil {
		return oaLock{}, false
	}
	var lock oaLock
	if err := json.Unmarshal(data, &lock); err != nil {
		return oaLock{}, false
	}
	if strings.TrimSpace(lock.LocalModel) == "" {
		return oaLock{}, false
	}
	return lock, true
}

// writeOALock 落一条会话的锁。已经锁着就不重写——锁是一次性的事实,重写只会把
// "什么时候第一次读的 OA"这条排查线索抹掉。
func writeOALock(workspace, sessionID, localModel string) error {
	path := oaLockPath(workspace, sessionID)
	if path == "" || strings.TrimSpace(localModel) == "" {
		return nil
	}
	if _, exists := readOALock(workspace, sessionID); exists {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(oaLock{LocalModel: strings.TrimSpace(localModel), At: time.Now().UTC()})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// oaModelSwitchAllowed 判断一条锁着的会话能不能换到 target 模型。
//
// 纯函数,因为这条规则要被三个入口共用(对话内切模型、设置页保存、会话恢复),
// 三处各判一次必然分叉,而分叉的那一处就是泄漏点。
//
// locked 为空表示没锁,一律放行。锁着时只允许"换成它自己"(幂等,比如设置页保存
// 时把当前模型原样写回)。
func oaModelSwitchAllowed(locked, target string) bool {
	locked = strings.TrimSpace(locked)
	if locked == "" {
		return true
	}
	return strings.EqualFold(locked, strings.TrimSpace(target))
}

// oaLockedSwitchError 是被锁拦下时给用户看的话。
//
// 它必须说清三件事:为什么不让换(不是坏了)、换了会怎样(整段历史会被发出去)、
// 以及**能怎么办**(开新会话)。少了最后一条,用户只会觉得这个应用坏了。
func oaLockedSwitchError(locked string) error {
	return &oaLockError{locked: strings.TrimSpace(locked)}
}

type oaLockError struct{ locked string }

func (e *oaLockError) Error() string {
	return "本会话已读取过 OA 数据，只能继续使用本地模型 " + e.locked +
		"。切换到其它模型会把包含 OA 内容的整段对话历史发送过去；如需换模型，请新建一个会话。"
}
