//go:build darwin || linux

package desktop

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

// withStubKey 把主密钥换成一把固定的，让加解密不依赖真实的系统钥匙串。
//
// 不这么做的话这组测试在 CI 上会去调 security / secret-tool：Linux runner 上根本
// 没装，macOS runner 的登录钥匙串也未必解锁，于是测的就不是加密逻辑而是环境。
func withStubKey(t *testing.T) {
	t.Helper()
	prev := masterKeyOnce
	key := bytes.Repeat([]byte{7}, 32)
	masterKeyOnce = func() ([]byte, bool) { return key, true }
	t.Cleanup(func() { masterKeyOnce = prev })
}

// TestSecretRoundTrip 加密再解密要拿回原文，含中文与长字符串。
func TestSecretRoundTrip(t *testing.T) {
	withStubKey(t)

	for _, plain := range []string{
		"sk-abc123",
		"带中文的令牌值",
		strings.Repeat("x", 4096),
		" 前后有空格 ",
	} {
		sealed, ok := protectSecret(plain)
		if !ok {
			t.Fatalf("protectSecret(%.20q) 失败", plain)
		}
		if strings.Contains(sealed, plain) {
			t.Errorf("密文里能直接看到明文: %.40q", sealed)
		}
		got, ok := unprotectSecret(sealed)
		if !ok || got != plain {
			t.Errorf("往返后 = %.20q (ok=%v)，期望 %.20q", got, ok, plain)
		}
	}
}

// TestSecretCiphertextDiffersEachTime 同一段明文两次加密必须给出不同密文。
//
// 盯的是 nonce 真的每次重新随机：GCM 在同一把密钥下重用 nonce 会直接泄露明文异或，
// 而这种错误从功能上完全看不出来——往返照样成功。
func TestSecretCiphertextDiffersEachTime(t *testing.T) {
	withStubKey(t)

	first, ok1 := protectSecret("same-input")
	second, ok2 := protectSecret("same-input")
	if !ok1 || !ok2 {
		t.Fatal("protectSecret 失败")
	}
	if first == second {
		t.Error("两次加密得到相同密文，nonce 没有重新随机")
	}
}

// TestSecretRejectsTampering 密文被改过就必须解不开，而不是给出一段垃圾明文。
//
// 这正是选 GCM 而不是裸 CTR 的理由：认证标签让改动无法蒙混过关。调用方拿到
// ok=false 会当作「没存过」，比拿到一段看似有效的坏令牌去调接口安全得多。
func TestSecretRejectsTampering(t *testing.T) {
	withStubKey(t)

	sealed, ok := protectSecret("original")
	if !ok {
		t.Fatal("protectSecret 失败")
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		t.Fatalf("解 base64: %v", err)
	}
	// 翻转密文最后一个字节（认证标签的一部分）。
	raw[len(raw)-1] ^= 0xff
	if got, ok := unprotectSecret(base64.StdEncoding.EncodeToString(raw)); ok {
		t.Errorf("篡改过的密文被接受了，解出 %q", got)
	}
}

// TestSecretRejectsGarbage 各种不成形的输入都不能 panic，一律 ok=false。
func TestSecretRejectsGarbage(t *testing.T) {
	withStubKey(t)

	for _, bad := range []string{
		"",
		"not-base64!!!",
		base64.StdEncoding.EncodeToString([]byte("short")),  // 比 nonce 还短
		base64.StdEncoding.EncodeToString(make([]byte, 12)), // 只有 nonce、没有密文体
	} {
		if got, ok := unprotectSecret(bad); ok {
			t.Errorf("unprotectSecret(%.20q) 不该成功，却给出 %q", bad, got)
		}
	}
	if _, ok := protectSecret(""); ok {
		t.Error("空明文不该被加密")
	}
}

// TestSecretWithoutKeyringDoesNotPersist 拿不到钥匙串时必须报 ok=false。
//
// 这条是安全边界：调用方据此把凭据从 desktop.json 里丢掉。若这里改成"退回明文存"，
// 症状是没有任何报错、文件看着也正常，只是凭据从此明文躺在磁盘上。
func TestSecretWithoutKeyringDoesNotPersist(t *testing.T) {
	prev := masterKeyOnce
	masterKeyOnce = func() ([]byte, bool) { return nil, false }
	t.Cleanup(func() { masterKeyOnce = prev })

	if _, ok := protectSecret("secret"); ok {
		t.Error("没有钥匙串时 protectSecret 不该成功")
	}
	if _, ok := unprotectSecret("whatever"); ok {
		t.Error("没有钥匙串时 unprotectSecret 不该成功")
	}
}
