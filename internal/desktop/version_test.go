package desktop

import "testing"

// TestCompareVersions 盯住版本比较的三条规则。它决定「要不要劝用户装东西」，
// 判错的两个方向都难看：判大了会反复劝装同一个版本，判小了新版永远推不动。
func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		// 基本序
		{"0.2.0", "0.1.0", 1},
		{"0.1.0", "0.2.0", -1},
		{"1.0.0", "1.0.0", 0},
		{"1.10.0", "1.9.0", 1}, // 按数值不是按字典序：1.10 > 1.9
		{"2.0.0", "1.99.99", 1},

		// v 前缀与构建元数据不参与比较
		{"v0.2.0", "0.2.0", 0},
		{"0.2.0+build.7", "0.2.0", 0},

		// 缺位的段按 0 算：发布时少写一段不该被当成降级
		{"1.2", "1.2.0", 0},
		{"1.2.1", "1.2", 1},

		// 预发布小于同号正式版；开发构建因此比一切正式版都小
		{"0.2.0-rc.1", "0.2.0", -1},
		{"0.2.0", "0.2.0-rc.1", 1},
		{"0.2.0-rc.1", "0.2.0-rc.2", -1},
		{"0.2.0-rc.2", "0.2.0-rc.10", -1}, // 预发布里的数字段也按数值比
		{"0.0.0-dev", "0.1.0", -1},
		{"0.0.0-dev", "0.0.0", -1},

		// 解不出数字的段按 0 算：宁可判成「相等、不提示」
		{"1.2.x", "1.2.0", 0},
		{"", "0.0.0", 0},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d，期望 %d", c.a, c.b, got, c.want)
		}
	}
}

// TestCompareVersionsIsAntisymmetric 反向比较必须给出相反的号。
//
// 单独验它是因为这个性质一旦破了，症状是「A 说有新版、B 说已是最新」这种自相矛盾
// 的状态，而从任何一次单独的调用里都看不出来。
func TestCompareVersionsIsAntisymmetric(t *testing.T) {
	versions := []string{"0.0.0-dev", "0.1.0", "0.2.0-rc.1", "0.2.0", "1.0.0", "1.10.2"}
	for _, a := range versions {
		for _, b := range versions {
			if got, rev := compareVersions(a, b), compareVersions(b, a); got != -rev {
				t.Errorf("compareVersions(%q,%q)=%d 与反向的 %d 不互为相反数", a, b, got, rev)
			}
		}
	}
}

// TestDefaultVersionIsOlderThanAnyRelease 开发构建必须比任何正式版都旧。
//
// 这条保证的是「检查更新」这条链路在开发机上就能整条走通——否则它只有在打过包的
// 机器上才验得了，而那正是最不方便调试的地方。
func TestDefaultVersionIsOlderThanAnyRelease(t *testing.T) {
	for _, released := range []string{"0.0.1", "0.1.0", "1.0.0"} {
		if compareVersions(appVersion, released) >= 0 {
			t.Errorf("默认版本 %q 不比正式版 %q 旧", appVersion, released)
		}
	}
}
