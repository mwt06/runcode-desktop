package main

import (
	"go/constant"
	"go/token"
	"go/types"
	"testing"
)

// parseJSONTag 必须对齐 encoding/json 的 tag 语义——尤其是 `,string`（值在线上是
// JSON 字符串，常用于保 64 位整数精度）与 `-,`（字段名字面量为 "-"）；漏掉任何一个
// 都会静默生成错误的 TS 类型（审核问题 #9）。
func TestParseJSONTag(t *testing.T) {
	cases := []struct {
		tag, goName   string
		name          string
		optional      bool
		stringEncoded bool
		skip          bool
	}{
		{"id", "ID", "id", false, false, false},
		{"id,omitempty", "ID", "id", true, false, false},
		{"id,string", "ID", "id", false, true, false},
		{"id,omitempty,string", "ID", "id", true, true, false},
		{"", "Field", "Field", false, false, false},
		{",omitempty", "Field", "Field", true, false, false},
		{"-", "Field", "", false, false, true},
		{"-,", "Field", "-", false, false, false}, // 字面量 "-" 字段名，不是跳过
	}
	for _, c := range cases {
		name, optional, stringEncoded, skip := parseJSONTag(c.tag, c.goName)
		if name != c.name || optional != c.optional || stringEncoded != c.stringEncoded || skip != c.skip {
			t.Errorf("parseJSONTag(%q, %q) = (%q, %v, %v, %v), want (%q, %v, %v, %v)",
				c.tag, c.goName, name, optional, stringEncoded, skip, c.name, c.optional, c.stringEncoded, c.skip)
		}
	}
}

// `,string` 只对 string/bool/整数/浮点（及其指针）生效，encoding/json 对其他类型
// 忽略该选项——TS 覆盖也必须同样收敛，否则会把不受影响的字段错标成 string。
func TestIsStringEncodable(t *testing.T) {
	named := types.NewNamed(types.NewTypeName(token.NoPos, nil, "TokenCount", nil), types.Typ[types.Int64], nil)
	cases := []struct {
		typ  types.Type
		want bool
	}{
		{types.Typ[types.Int64], true},
		{types.Typ[types.String], true},
		{types.Typ[types.Bool], true},
		{types.Typ[types.Float64], true},
		{types.NewPointer(types.Typ[types.Int64]), true},
		{named, true}, // 命名类型看底层
		{types.NewStruct(nil, nil), false},
		{types.NewSlice(types.Typ[types.Byte]), false},
	}
	for _, c := range cases {
		if got := isStringEncodable(c.typ); got != c.want {
			t.Errorf("isStringEncodable(%s) = %v, want %v", c.typ, got, c.want)
		}
	}
}

// 未分组常量的豁免按声明类型判断：CommandKind 是传输元数据（kinds 走命令表的
// 同步校验），未命名/基础类型的字符串常量不豁免——它们要么进组要么报错（审核问题 #10）。
func TestConstTypeNameForExemption(t *testing.T) {
	kind := types.NewNamed(types.NewTypeName(token.NoPos, nil, "CommandKind", nil), types.Typ[types.String], nil)
	typed := types.NewConst(token.NoPos, nil, "CommandQuery", kind, constant.MakeString("query"))
	if got := constTypeName(typed); got != "CommandKind" {
		t.Errorf("constTypeName(typed) = %q, want CommandKind", got)
	}
	if !constExemptTypes[constTypeName(typed)] {
		t.Error("CommandKind constants should be exempt")
	}
	untyped := types.NewConst(token.NoPos, nil, "EventFoo", types.Typ[types.UntypedString], constant.MakeString("foo"))
	if got := constTypeName(untyped); got != "" {
		t.Errorf("constTypeName(untyped) = %q, want empty", got)
	}
	if constExemptTypes[constTypeName(untyped)] {
		t.Error("untyped string constants must not be exempt")
	}
}

// 最长前缀优先：ToolEvent* 不能被误归入 Event* 组。
func TestMatchGroupLongestPrefix(t *testing.T) {
	cases := []struct {
		name string
		want string // goPrefix；空 = 无匹配
	}{
		{"ToolEventStarted", "ToolEvent"},
		{"EventTurnEnd", "Event"},
		{"DecisionAllow", "Decision"},
		{"ErrCodeBusy", "ErrCode"},
		{"CommandQuery", ""},
	}
	for _, c := range cases {
		grp := matchGroup(c.name)
		got := ""
		if grp != nil {
			got = grp.goPrefix
		}
		if got != c.want {
			t.Errorf("matchGroup(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}
