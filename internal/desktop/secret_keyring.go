//go:build darwin || linux

package desktop

// macOS 与 Linux 的凭据保护。
//
// Windows 那边用 DPAPI，它是**纯加密**：给一段明文、拿回一段密文，密文存哪儿由调用
// 方决定（这里是 desktop.json）。而 macOS 的钥匙串与 Linux 的 Secret Service 是
// **存储服务**：存的是 key-value，语义对不上。
//
// 所以这里加一层：钥匙串里只放一把随机生成的主密钥，凭据本身仍用 AES-GCM 加密后
// 留在 desktop.json 里。这样 protectSecret / unprotectSecret 的契约与 DPAPI 完全
// 一致——调用方不必知道平台差异——而钥匙串里始终只有一条记录，不会随着用户改几次
// API key 就堆出一堆垃圾条目。
//
// 安全性来自哪里：主密钥由系统钥匙串保管，绑定当前用户账号（macOS 的钥匙串、
// Linux 的 gnome-keyring/kwallet 都是这个模型），别的用户读不到，于是 desktop.json
// 即便被复制走也解不开。**没有钥匙串可用时一律返回 ok=false**，调用方于是不落盘，
// 让用户重新输入——把密钥和密文一起明文放在同一台机器上，安全性并不比不存更好，
// 只是看起来像做了事。

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"io"
	"sync"
)

const (
	// keyringService / keyringAccount 是主密钥在钥匙串里的坐标。两个平台用同一对
	// 值，方便排查（macOS 上「钥匙串访问」搜 runcode 就能看到那一条）。
	keyringService = "runcode-desktop"
	keyringAccount = "master-key"
)

// masterKeyOnce 缓存主密钥：每次读写凭据都去调一次外部命令（security /
// secret-tool）会明显拖慢启动，而密钥在进程生命周期内不会变。
//
// 缓存的是「取到了没有」这件事本身——取不到时同样缓存，避免在没有钥匙串的环境里
// 每保存一个字段就重试一次外部命令。
var masterKeyOnce = sync.OnceValues(loadOrCreateMasterKey)

// loadOrCreateMasterKey 从系统钥匙串取主密钥，没有就生成一把存进去。
func loadOrCreateMasterKey() ([]byte, bool) {
	if raw, ok := keyringGet(keyringService, keyringAccount); ok {
		if key, err := base64.StdEncoding.DecodeString(raw); err == nil && len(key) == 32 {
			return key, true
		}
		// 钥匙串里的值坏了（手工改过、或早期版本写的别的格式）。重新生成并覆盖，
		// 而不是就此报废：坏值的唯一后果是旧密文解不开，那些凭据本来也读不出来了。
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, false
	}
	if !keyringSet(keyringService, keyringAccount, base64.StdEncoding.EncodeToString(key)) {
		return nil, false
	}
	return key, true
}

// protectSecret 用主密钥把 s 加密成 base64 密文。ok=false 时调用方不落盘。
func protectSecret(s string) (string, bool) {
	if s == "" {
		return "", false
	}
	key, ok := masterKeyOnce()
	if !ok {
		return "", false
	}
	gcm, ok := newGCM(key)
	if !ok {
		return "", false
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", false
	}
	// nonce 拼在密文前面：解密时要用它，而它不需要保密。
	sealed := gcm.Seal(nonce, nonce, []byte(s), nil)
	return base64.StdEncoding.EncodeToString(sealed), true
}

// unprotectSecret 是 protectSecret 的逆操作。
func unprotectSecret(protected string) (string, bool) {
	if protected == "" {
		return "", false
	}
	key, ok := masterKeyOnce()
	if !ok {
		return "", false
	}
	gcm, ok := newGCM(key)
	if !ok {
		return "", false
	}
	raw, err := base64.StdEncoding.DecodeString(protected)
	if err != nil || len(raw) < gcm.NonceSize() {
		return "", false
	}
	nonce, body := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		// 认证失败：换过机器、换过用户、或者密文被改过。都不是能恢复的情况，
		// 让调用方当作「没有存过」处理。
		return "", false
	}
	return string(plain), true
}

func newGCM(key []byte) (cipher.AEAD, bool) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, false
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, false
	}
	return gcm, true
}
