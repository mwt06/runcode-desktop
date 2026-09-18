//go:build darwin

package desktop

func secretHint() (reason, fix string) {
	return "系统钥匙串不可用：登录钥匙串可能被锁定，或本应用被拒绝访问。",
		"在「钥匙串访问」里解锁「登录」钥匙串，然后重启本应用。"
}
