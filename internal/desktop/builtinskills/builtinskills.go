// Package builtinskills carries the skills that ship inside the binary and are
// materialised into the user's skills directory on first run.
//
// 为什么要嵌进二进制、而不是让用户去市场装：这几个是**产品答应了的能力**——比如
// 录音纪要收尾时要按国开模板出会议纪要，那条链路必须开箱即用。走市场意味着它依赖
// 登录、租户、manageapi 授权和一个时好时坏的下载接口，任何一环没就位，用户看到的
// 是"录音纪要生成的纪要格式不对"，而不是"你还没装那个技能"。
//
// 与市场技能装到同一个地方（用户级技能目录），所以插件页照样能看、能停用、能编辑——
// 它不是另一套并行的技能体系，只是获取途径不同。
package builtinskills

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"path"
	"sort"
)

// files 是随二进制发布的技能树：skills/<名字>/SKILL.md + 随附文件。
//
// 用 all: 前缀是必须的：默认模式会跳过以 . 或 _ 开头的文件与目录，而技能包里出现
// 这类文件是完全正常的（.gitignore、_assets）。宁可多打包几个字节，也不要出现
// "本地跑得好好的，装机之后少一个文件"这种只在成品上才显形的差异。
//
//go:embed all:skills
var files embed.FS

// Skill 是一个待安装的内置技能。
type Skill struct {
	// Name 是技能名，也是落到磁盘上的目录名。
	Name string
	// Digest 是这份内容的指纹，安装记录按它判断"要不要重装"。
	//
	// 用内容哈希而不是手写版本号：版本号得记着改，忘了改就是"发了新版但用户机器上
	// 还是旧的"，而这种故障没有任何报错。哈希是内容自己算出来的，改了文件它必然变。
	Digest string
	// FS 是以这个技能目录为根的文件系统，直接 fs.WalkDir 拷出去即可。
	FS fs.FS
}

// All 列出所有内置技能，按名字排序（安装顺序稳定，日志和测试才好读）。
func All() ([]Skill, error) {
	entries, err := fs.ReadDir(files, "skills")
	if err != nil {
		return nil, err
	}
	out := make([]Skill, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub, err := fs.Sub(files, path.Join("skills", e.Name()))
		if err != nil {
			return nil, err
		}
		digest, err := digestOf(sub)
		if err != nil {
			return nil, err
		}
		out = append(out, Skill{Name: e.Name(), Digest: digest, FS: sub})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// digestOf 算一棵目录树的内容指纹：路径与内容都算进去。
//
// 路径也要算：只哈希内容的话，把一个文件改名（内容不变）指纹不变，用户机器上那份
// 就永远停在旧的文件名上。fs.WalkDir 按字典序遍历，所以同一份内容每次都得到同一个
// 指纹——这是记录能拿来做比对的前提。
func digestOf(fsys fs.FS) (string, error) {
	h := sha256.New()
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(data)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
