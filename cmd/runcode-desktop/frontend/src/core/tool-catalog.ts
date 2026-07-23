// 内置工具的中文目录：一处定义，两种呈现——
//   verb  对话流工具行里的短动词（后面紧跟 mono 目标，所以要短）
//   label/desc  插件管理页里的完整名称与说明
// 仅用于界面展示：发送给模型的工具定义、名称与描述全部保持原样(英文)，因此模型
// 的工具调用判断不受影响。MCP / 用户自定义工具不在此表内，按原样显示。
//
// 拆分前 chat-view 的 VERB 与 pages 的 TOOL_ZH 是两份各自维护的字典，同一个工具
// 要改两处；合并到这里后二者只是同一条目的两个字段。
export type BuiltinTool = { label: string; desc: string; verb?: string }

export const BUILTIN_TOOLS: Record<string, BuiltinTool> = {
  Read: { verb: '读取文件', label: '读取文件', desc: '读取文件内容。文本返回带行号的内容；图片(png/jpg/jpeg/gif/webp)直接返回图像。' },
  Write: { verb: '写入文件', label: '写入文件', desc: '创建或覆盖写入一个文件。' },
  Edit: { verb: '编辑', label: '编辑文件', desc: '在已读取的文件里做精确文本替换。' },
  Delete: { verb: '删除', label: '删除', desc: '删除工作区里的文件或目录，默认移入回收站(可恢复)，permanent=true 则不可逆删除。' },
  Glob: { verb: '查找文件', label: '查找文件', desc: '按 glob 通配符查找工作区文件。' },
  Grep: { verb: '搜索项目代码', label: '搜索代码', desc: '用正则搜索工作区文件内容，支持内容/文件名/计数等输出模式、上下文行与多行匹配。' },
  Bash: { verb: '运行命令', label: '运行命令', desc: '在工作区执行非交互 shell 命令(需授权，Windows 用 cmd，其余用 bash)。' },
  BashOutput: { verb: '后台输出', label: '后台命令输出', desc: '读取后台运行命令的新增输出。' },
  KillShell: { verb: '终止命令', label: '终止后台命令', desc: '终止一个后台运行的命令。' },
  TodoWrite: { verb: '规划任务', label: '规划任务', desc: '记录当前任务清单，每次传完整列表并替换上一次。' },
  WebFetch: { verb: '抓取网页', label: '抓取网页', desc: '抓取一个网址并按提示词处理其内容。' },
  WebSearch: { verb: '联网搜索', label: '联网搜索', desc: '通过搜索引擎联网检索并返回结果(标题、网址、摘要)。' },
  Wait: { verb: '等待', label: '等待', desc: '暂停指定秒数再继续——等待构建、部署或后台命令等外部操作稳定后再检查。' },
  GetCurrentTime: { verb: '获取当前时间', label: '获取当前时间', desc: '返回当前日期与时间(本地与 UTC、所在时区及 RFC3339 时间戳)。' },
  Remember: { verb: '记录记忆', label: '记录记忆', desc: '把跨会话有用的事实写入持久记忆(用户级或项目级)，下次会话自动带上。' },
  Analyze: { verb: '结构化分析', label: '结构化分析', desc: '为当前思考协议记录结构化分析。' },
  // AskUser 有意不给 verb：它的工具行以原名显示（提问本身由 AskCard 呈现）。
  AskUser: { label: '询问用户', desc: '向用户提问并停下等待回复，用于需要用户决策或缺少关键信息时。' },
  open_preview: { verb: '预览', label: '预览产物', desc: '在桌面预览面板打开工作区文件(仅桌面版)。' },
  Task: { verb: '委派子代理', label: '委派子代理', desc: '把一个自包含的子任务委派给子代理独立执行。' },
  Skill: { verb: '加载技能', label: '加载技能', desc: '加载并执行一个已定义的技能。' },
}

// toolVerb is the短动词 for a tool row; unmapped tools (MCP, custom) keep their
// own name, and a nameless event falls back to a generic label.
export function toolVerb(name?: string): string {
  return BUILTIN_TOOLS[name || '']?.verb || name || '工具'
}
