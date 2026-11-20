package desktop

import "encoding/base64"

// imageGenJWT：无签名令牌，claims 只给 tokenIdentity 稳定账号指纹；真鉴权在网关，不在本客户端。
func imageGenJWT(sub string) string {
	return "x." + base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"image-generation-test","sub":"`+sub+`"}`)) + ".x"
}
