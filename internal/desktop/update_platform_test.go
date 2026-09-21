//go:build !kylin

package desktop

import "runtime"

// wantUpdateOS 是测试对平台键前一半的独立预期，故意不引用 updateOS 本身——
// 引用它的话，把 kylin10 写错成别的什么，测试也照样是绿的。
const wantUpdateOS = runtime.GOOS
