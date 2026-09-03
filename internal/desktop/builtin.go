package desktop

// 内置技能的落盘：把随二进制发布的技能写进用户级技能目录。
//
// 装到与市场技能同一个地方（userResourceDir(kindSkills)），所以插件页照样能看、
// 能停用、能编辑——它不是另一套并行体系，只是获取途径不同。

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/wt68/runcode/internal/desktop/builtinskills"
)

// builtinRecordName 是安装记录的文件名，放在用户配置目录下（与 desktop.json 同级）。
//
// **不能放进技能目录里面**：用户在插件页把这个技能删了，记录得留下来，否则下次启动
// 又给他装回去——一个删不掉的东西比一个没装上的东西烦人得多。
const builtinRecordName = "builtin-skills.json"

// installBuiltinSkills 把内置技能写进用户级技能目录，返回实际写了哪些。
//
// 判据有两条，任一成立就写：
//   - 记录里的指纹 != 当前指纹 → 头一次启动，或我们发了新版；
//   - 目录不在 → 不管记录怎么说，它现在**不在**，装回去。
//
// 第二条是后补的，补的是一个真实故障：记录里写着"装过"、磁盘上却没有这个目录，于是
// 每次启动都跳过，录音纪要那条链路永远拿不到国开模板、悄悄退回通用格式——而界面上
// 没有任何地方会说它不在。原来那版把「目录不在」当成"用户删掉了，尊重他"，但这两件事
// 从记录里根本分辨不出来：半截的升级、杀软隔离、换电脑迁配置、手动删目录，看起来
// 都一模一样。内置技能是随应用发布的东西，它该像程序文件一样，缺了就补回来。
//
// 那"我不想要它"怎么办？用停用，不用删除——停用是应用能持久观察到的状态，删除不是。
// DeleteSkill 删掉一个内置技能时会顺手把它停用，所以用户的意图不会被这次重装吃掉。
//
// 指纹变化会盖掉用户的改动。这是内置内容更新的常规取舍，也是唯一能让"修好的模板
// 真的到用户手上"的做法；想保住自己的改法，把它另存成一个别的名字即可。
//
// 全程 best effort：装不上只记诊断日志。内置技能是锦上添花，不该让应用起不来。
func installBuiltinSkills() []string {
	root := userResourceDir(kindSkills)
	if root == "" {
		return nil
	}
	skills, err := builtinskills.All()
	if err != nil {
		debugLog("builtin skills: %v", err)
		return nil
	}
	record := loadBuiltinRecord()
	var wrote []string
	for _, sk := range skills {
		if record[sk.Name] == sk.Digest && skillDirPresent(root, sk.Name) {
			continue
		}
		dest := filepath.Join(root, sk.Name)
		if err := writeSkillTree(sk.FS, dest); err != nil {
			debugLog("builtin skill %s: %v", sk.Name, err)
			continue
		}
		record[sk.Name] = sk.Digest
		wrote = append(wrote, sk.Name)
	}
	if len(wrote) > 0 {
		saveBuiltinRecord(record)
	}
	return wrote
}

// isBuiltinSkill 报告一个技能名是不是随二进制发布的内置技能。它决定"删除"在这个名字
// 上还意味着什么（见 DeleteSkill）：内置的删不干净，所以删除要顺带停用。
func isBuiltinSkill(name string) bool {
	skills, err := builtinskills.All()
	if err != nil {
		debugLog("builtin skills: %v", err)
		return false
	}
	for _, sk := range skills {
		if sk.Name == name {
			return true
		}
	}
	return false
}

// skillDirPresent 报告一个技能目录是否真的还在磁盘上。判据是 SKILL.md 而不是目录
// 本身：一个只剩空壳的目录（拷贝中断、杀软删了正文）加载不出技能，和没有一样，
// 而"目录在"会让它永远得不到修复。
func skillDirPresent(root, name string) bool {
	info, err := os.Stat(filepath.Join(root, name, "SKILL.md"))
	return err == nil && info.Mode().IsRegular()
}

// writeSkillTree 把一棵嵌入的技能树拷到 dest。
//
// 先解到同级的临时目录再整体替换：中途失败（磁盘满、文件被占用）不会留下一个半截
// 的技能目录——那种目录 SKILL.md 可能在、随附脚本却少一半，加载得起来但跑一半报错，
// 比干脆没装难查得多。
func writeSkillTree(src fs.FS, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(filepath.Dir(dest), ".builtin-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()

	err = fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(staging, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	return os.Rename(staging, dest)
}

// builtinRecordPath 是安装记录的路径；用户配置目录取不到时返回空串。
func builtinRecordPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "runcode", builtinRecordName)
}

// loadBuiltinRecord 读安装记录：技能名 → 已装内容的指纹。读不出来一律当空，
// 大不了重装一次，比因为一个坏掉的记录文件让内置技能永远装不上强。
func loadBuiltinRecord() map[string]string {
	out := map[string]string{}
	p := builtinRecordPath()
	if p == "" {
		return out
	}
	data, err := os.ReadFile(p) //nolint:gosec // 路径来自 os.UserConfigDir，不是用户输入
	if err != nil {
		return out
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]string{}
	}
	return out
}

func saveBuiltinRecord(record map[string]string) {
	p := builtinRecordPath()
	if p == "" {
		return
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	if err := os.WriteFile(p, data, 0o600); err != nil {
		debugLog("builtin skills record: %v", err)
	}
}
