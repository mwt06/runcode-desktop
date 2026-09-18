package desktop

// sudo 的 askpass：sudo 要密码时，由本应用弹框问用户。与平台无关的那一半。
//
// 链路：sudo（没有终端）→ 执行 SUDO_ASKPASS 指向的程序，也就是**本应用自己**
// （RunAskpass，见 askpass_linux.go）→ 它经本机套接字连回正在运行的本应用 → 这里核验
// 来者、弹框、等用户 → 密码原路交回 → sudo 从 askpass 的标准输出读走。
//
// 为什么 askpass 是本应用自己而不是另写一个脚本：麒麟的 KYSEC 会拦下新写出来的脚本
// （实测 /tmp 下的直接"权限不够"），而本应用的二进制是经 deb 装进来的、KYSEC 认它。
//
// # 密码只走一条路
//
// 用户的键盘 → 本应用 → 套接字 → askpass 进程的标准输出 → sudo。它**不进**事件流、
// 工具输出、日志或任何落盘文件；交出去之后内存里那份立刻清零。这件事要紧的程度高于
// "root 权限"本身：这台机器的登录密码很可能就是单位的统一身份密码。

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wt68/runcode/internal/protocol"
)

const (
	// askpassTimeout 是等用户输密码的上限。超时就当取消——sudo 那边会报"没有提供密码"，
	// 模型看得到这句，用户回来时也不会面对一个不知道为谁而开的密码框。
	askpassTimeout = 3 * time.Minute

	// 这几个环境变量只注进工具子进程（engine.Config.ToolEnv），从不设进本进程。
	envAskpassSocket  = "RUNCODE_ASKPASS_SOCKET"
	envAskpassToken   = "RUNCODE_ASKPASS_TOKEN"
	envAskpassSession = "RUNCODE_ASKPASS_SESSION"
)

var (
	errAskpassNotArmed = errors.New("这条 sudo 命令没有经过你在应用里的确认，拒绝提供密码")
	errAskpassCanceled = errors.New("已取消")
	errAskpassTimeout  = errors.New("等待输入密码超时")
)

// askpassBroker 管着"正在等用户输密码"的那些请求。
type askpassBroker struct {
	emit    func(name string, payload any)
	gate    *privilegeGate
	timeout time.Duration

	mu      sync.Mutex
	pending map[string]chan []byte // nil 值 = 取消
}

func newAskpassBroker(emit func(string, any), gate *privilegeGate) *askpassBroker {
	return &askpassBroker{emit: emit, gate: gate, timeout: askpassTimeout, pending: map[string]chan []byte{}}
}

// ask 替 sudo 向用户要一次密码，阻塞到用户回答、取消、超时或 ctx 结束。
//
// command 是 sudo **实际**要执行的命令行（取自 sudo 进程本身），原样显示给用户。
func (b *askpassBroker) ask(ctx context.Context, session, command, prompt string) ([]byte, error) {
	if b.gate == nil || !b.gate.armed(session) {
		return nil, errAskpassNotArmed
	}
	id := randomID()
	ch := make(chan []byte, 1)
	b.mu.Lock()
	b.pending[id] = ch
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.pending, id)
		b.mu.Unlock()
		// 无论怎么结束都撤回一次：用户可能开着两个窗口，或者弹框还没关请求就超时了。
		b.emit(protocol.EventAskpassDone, protocol.AskpassDone{ID: id})
	}()
	b.emit(protocol.EventAskpassRequest, protocol.AskpassRequest{
		ID: id, SessionID: session, Command: command, Prompt: prompt,
	})

	timer := time.NewTimer(b.timeout)
	defer timer.Stop()
	select {
	case pw := <-ch:
		if pw == nil {
			return nil, errAskpassCanceled
		}
		return pw, nil
	case <-timer.C:
		return nil, errAskpassTimeout
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// answer 交回用户输入的密码。请求已经不在（超时、取消、重复回答）时静默丢弃。
func (b *askpassBroker) answer(id, password string) {
	b.deliver(id, []byte(password))
}

// cancel 取消一次请求。
func (b *askpassBroker) cancel(id string) {
	b.deliver(id, nil)
}

func (b *askpassBroker) deliver(id string, pw []byte) {
	b.mu.Lock()
	ch := b.pending[id]
	delete(b.pending, id)
	b.mu.Unlock()
	if ch == nil {
		zero(pw)
		return
	}
	ch <- pw // 带缓冲，且只会被投递一次（上面已从表里删掉）
}

// zero 就地清零。Go 的字符串清不掉，所以密码一律以 []byte 流转，用完即清。
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

func randomID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// ---- /proc 的解析（纯函数，放在这里好让所有平台都能测）------------------------

// procStatus 是 /proc/<pid>/status 里我们关心的三项。
type procStatus struct {
	name string
	ppid int
	euid int
}

// parseProcStatus 解析 /proc/<pid>/status。
//
// 取**有效** UID（Uid 行的第二列）而不是真实 UID：sudo 是 setuid root，真实 UID 是
// 调用者、有效 UID 才是 0。来者核验靠的正是这一点——一个名叫 sudo 的普通脚本改得了
// 自己的名字（实测 Name 会显示 sudo），改不了有效 UID。
func parseProcStatus(text string) (procStatus, bool) {
	st := procStatus{ppid: -1, euid: -1}
	for _, line := range strings.Split(text, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(val)
		if len(fields) == 0 {
			continue
		}
		switch key {
		case "Name":
			st.name = fields[0]
		case "PPid":
			if n, err := strconv.Atoi(fields[0]); err == nil {
				st.ppid = n
			}
		case "Uid":
			if len(fields) >= 2 {
				if n, err := strconv.Atoi(fields[1]); err == nil {
					st.euid = n
				}
			}
		}
	}
	return st, st.name != "" && st.ppid >= 0 && st.euid >= 0
}

// cmdlineString 把 /proc/<pid>/cmdline（NUL 分隔）变成一行可读的命令。
func cmdlineString(raw []byte) string {
	parts := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
	return strings.Join(parts, " ")
}

// askpassServer 是正在监听的那个本机套接字（具体实现分平台，见 askpass_linux.go）。
type askpassServer struct {
	// path 是套接字路径，token 是注给工具子进程的口令，exe 是本应用二进制的真实路径
	// ——它同时是 SUDO_ASKPASS 的值，也是来者核验比对的对象。
	path, token, exe string
	stop             func()
}

func (s *askpassServer) close() {
	if s != nil && s.stop != nil {
		s.stop()
	}
}

// ---- Wails 命令 ----------------------------------------------------------

// AnswerAskpass 交回用户在密码框里输入的密码。
func (a *App) AnswerAskpass(id, password string) {
	if a.askpass != nil {
		a.askpass.answer(strings.TrimSpace(id), password)
	}
}

// CancelAskpass 取消一次密码请求（sudo 那边会报"没有提供密码"）。
func (a *App) CancelAskpass(id string) {
	if a.askpass != nil {
		a.askpass.cancel(strings.TrimSpace(id))
	}
}

// askpassEnv 是注进一条会话工具子进程的环境变量；本平台不支持或服务没起来时为 nil。
func (a *App) askpassEnv(session string) map[string]string {
	if !a.askpassReady() {
		return nil
	}
	srv := a.askpassSrv.Load()
	return map[string]string{
		"SUDO_ASKPASS":    srv.exe,
		envAskpassSocket:  srv.path,
		envAskpassToken:   srv.token,
		envAskpassSession: session,
	}
}

// askpassReady 报告 sudo 这条路此刻走不走得通。
//
// 除了服务得起来，还要有 DISPLAY：sudo 在没有终端时自动改走 askpass，前提是 DISPLAY
// 非空（它拿这个当"有图形界面"的信号，并不真去连）。麒麟的 Wayland 会话经 XWayland
// 带着 DISPLAY=:0，正常就满足；真没有的环境里 sudo 会报"需要终端"——那就干脆不开放，
// 而不是伪造一个 DISPLAY：matplotlib 这类程序正是按它来挑图形后端的，给个假值会让
// 它们去连一个不存在的显示器。
func (a *App) askpassReady() bool {
	return a.askpassSrv.Load() != nil && os.Getenv("DISPLAY") != ""
}
