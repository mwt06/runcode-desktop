package desktop

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

type accountIdentity struct {
	Issuer, Subject, Digest string
	Stable                  bool
}

// accountIdentityForToken 是所有账号分区的唯一推导入口。
// JWT 使用 iss/sub 得到稳定摘要；不透明令牌只允许调用方显式提供登录周期回落。
func accountIdentityForToken(token, fallback string) accountIdentity {
	if issuer, subject := tokenIdentityParts(token); issuer != "" && subject != "" {
		return accountIdentity{
			Issuer: issuer, Subject: subject,
			Digest: digestVision([]string{issuer, subject}),
			Stable: true,
		}
	}
	if fallback == "" {
		return accountIdentity{}
	}
	return accountIdentity{Digest: digestVision(fallback)}
}

func tokenIdentity(token string) string {
	return accountIdentityForToken(token, "").Digest
}

// tokenIdentityParts reads stable identity claims for local partitioning, not
// authentication. Callers retain ownership of token locking and epoch checks.
func tokenIdentityParts(token string) (issuer, subject string) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", ""
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ""
	}
	var claims struct {
		Issuer  string `json:"iss"`
		Subject string `json:"sub"`
	}
	if json.Unmarshal(b, &claims) != nil || claims.Subject == "" {
		return "", ""
	}
	return claims.Issuer, claims.Subject
}
