// 内置场景：对话栏上方那排按分类分级选的常用任务。
//
// 数据来自产品侧的《内置场景》表（v2 8.25 终版）里「默认场景 = 是/新增」的那 45 行，
// 由 scripts 一次性导出后提交进来——**运行期不读 Excel**，这里就是唯一事实来源。
// 表更新了重新导一次，别手改：手改会和下一次导出打架。
//
// 为什么内置而不是拉服务端：起始页与输入框上方是首屏，未登录、离线、网关抖动时
// 都得画得出来。将来网桥提供 /api/scenarios 时按「内置一份兜底 + 拉到就覆盖」接，
// 与 syncMarketOnce / SkillMarket 的缓存回退是同一套路——所以 skill 字段现在就带着，
// 虽然本阶段用不到（见下）。

/** Scenario 是一条可以一键填进输入框的常用任务。 */
export interface Scenario {
  /** 稳定主键。有关联 skill 的直接用 skill 名，其余是 <分类 id>-<序号>。 */
  id: string
  name: string
  /** 默认提示词。含【】占位符，点选后由调用方选中第一个（见 firstPlaceholder）。 */
  prompt: string
  /** 一句话说明这个场景干什么，选择面板里显示。 */
  blurb: string
  /**
   * 关联的市场技能 id；没有关联的为空（OA 那三条走 MCP，录音纪要是内置功能）。
   *
   * 本阶段**不用**它：提示词本身就是完整可用的，装了对应技能只是效果更好，而技能
   * 该不该加载由引擎按 frontmatter 的 description 自行判断——这正是那一句的用途。
   * 留着它是为了下一阶段「未装则一键从市场装」不必再改一次数据。
   */
  skill: string
}

/** ScenarioCategory 是一级分类。 */
export interface ScenarioCategory {
  id: string
  name: string
  /** ui/icons 里的图标名。 */
  icon: string
  items: Scenario[]
}

export const SCENARIOS: ScenarioCategory[] = [
  {
    id: "oa", name: "OA查询", icon: "search",
    items: [
      {
        id: "oa-1",
        name: "查待办事项",
        prompt: "帮我查询OA系统里我最近的待办事项",
        blurb: "该mcp实现了跟国家开放大学OA系统数据查询的对接，通过OA系统登录认证并安装该mcp后，可按权限查询oa数据",
        skill: "",
      },
      {
        id: "oa-2",
        name: "查个人名片",
        prompt: "帮我查询OA系统里【人名】的个人名片信息",
        blurb: "该mcp实现了跟国家开放大学OA系统数据查询的对接，通过OA系统登录认证并安装该mcp后，可按权限查询oa数据",
        skill: "",
      },
      {
        id: "oa-3",
        name: "查最近通知",
        prompt: "帮我查询OA系统里最近的通知，看看哪些值得我关注",
        blurb: "该mcp实现了跟国家开放大学OA系统数据查询的对接，通过OA系统登录认证并安装该mcp后，可按权限查询oa数据",
        skill: "",
      },
    ],
  },
  {
    id: "writing", name: "帮我写作", icon: "pencil",
    items: [
      {
        id: "cn-docx",
        name: "写常规公文",
        prompt: "帮我基于【通知草稿/要点提纲】，起草一份党政机关公文，包含标题、主送机关、正文、发文机关署名与成文日期；如需份号、密级、紧急程度或签发人请一并告知。",
        blurb: "生成符合 GB/T 9704 党政公文或 GB/T 7713 学术论文排版的 Word 文档。自动设置公文专用字体字号、页面尺寸、页边距、行距和首行缩进。带 --verify 渲染自检（生成 PNG → Read 校验 tofu/缩进）。",
        skill: "cn-docx",
      },
      {
        id: "work-report",
        name: "写工作汇报",
        prompt: "帮我基于【工作台账/数据】，撰写一份工作汇报，包含：总体情况、重点工作成效、存在问题、下一步工作安排；请先告诉我汇报对象和重点方向。",
        blurb: "7 阶段工作流（需求理解→素材整理→调研补充→大纲建议→内容撰写→多视角审阅→定稿优化）生成 2000-5000 字行政公文风格工作汇报。含去痕处理 + 多 Agent 角色审阅。",
        skill: "work-report",
      },
      {
        id: "speech",
        name: "写领导讲话稿",
        prompt: "帮我撰写一份校领导在【XX会议】上的讲话稿，包含回顾总结、形势分析、重点任务部署、要求与号召；如有领导既往讲话稿请一并提供，以便学习其用语习惯。",
        blurb: "8.5 阶段工作流生成 800-8000 字领导讲话稿（部署动员/总结表彰/学术发言/座谈交流/礼仪致辞）。含试讲打磨环节 + 可学习讲话人既往文稿风格。",
        skill: "speech",
      },
      {
        id: "academic-speech",
        name: "写学术报告演讲稿",
        prompt: "帮我基于【论文/课题结题材料】，撰写一份学术会议报告演讲稿，包含研究缘起、方法与数据、核心发现、结论与局限；同时给出配套幻灯片大纲。",
        blurb: "会议报告/论文答辩/课题汇报/受邀讲座四类学术演讲。正向模式（研究→演讲稿+幻灯片大纲）与逆向模式（已有 PPT→配套讲稿）。融合 Winston/Alley/Duarte/Rosling/Doumont/Minto/Tufte 七位方法论。",
        skill: "academic-speech",
      },
      {
        id: "doc-coauthoring",
        name: "长文档写作",
        prompt: "帮我协作撰写一份【XX方案/报告】，请先和我确认目标读者、核心结论和章节框架，再逐章起草。",
        blurb: "三阶段结构化工作流（对齐框架→逐章共创→统稿定稿），协作撰写长文档、提案与规格说明，适合需要多轮打磨的方案类写作。",
        skill: "doc-coauthoring",
      },
    ],
  },
  {
    id: "recorder", name: "录音纪要", icon: "mic",
    items: [
      {
        id: "recorder-1",
        name: "录音纪要",
        prompt: "帮我把【会议录音】转成会议纪要，包含：议题与讨论要点、决议事项、待办清单（事项—责任人—完成时限）。",
        blurb: "客户端内置功能（非 skill）：本地语音转写＋说话人区分＋结构化纪要生成，音频不出本地。对应 v4「自建建议」P0「中文会议纪要（教研会／教务会／听课评课记录）」——四个市场交叉验证，端到端「语音→纪要」为空白。",
        skill: "",
      },
    ],
  },
  {
    id: "research", name: "调研研究", icon: "compass",
    items: [
      {
        id: "quick-research",
        name: "快速调研",
        prompt: "帮我快速调研【XX话题】，输出结构化报告，包含：主要做法与案例、关键争议、结论与建议，并标注信息来源。",
        blurb: "3 阶段（SCOPE-RETRIEVE-SYNTHESIZE）输出 2000-4000 字结构化报告，每条信息标注来源可信度等级。适用于选题评估、方案比选、趋势扫描、竞品概览等快速了解型场景。",
        skill: "quick-research",
      },
      {
        id: "hv-research",
        name: "深度调研",
        prompt: "帮我用横纵研究法深度调研【XX话题】：纵向梳理演进历程，横向对比当前格局，在两轴交汇处给出学术级洞察。",
        blurb: "索绪尔历时—共时双轴分析：纵轴追踪完整演进历程（锚定学术理论框架），横轴在当前截面做系统性横向对比，两轴交汇处产出学术级洞察。6 阶段流程，需联网检索一手来源。",
        skill: "hv-research",
      },
      {
        id: "research",
        name: "高可信来源调研",
        prompt: "帮我调研【XX话题】，只使用高可信度的一手来源，结果整理成Markdown",
        blurb: "高可信一手来源调研,结果落盘Markdown",
        skill: "research",
      },
      {
        id: "research-topic",
        name: "研究选题",
        prompt: "帮我把【XX】这个研究想法细化成规范选题：检索已有研究空白，拆解研究要素，评估可行性，给出研究问题。",
        blurb: "PICO 四要素＋FINER 五标准评估选题，把模糊研究想法转化为规范选题与研究问题。三种模式（快速／完整／选题优化），内置教育技术领域专题素材。",
        skill: "research-topic",
      },
      {
        id: "translate",
        name: "中英学术互译",
        prompt: "帮我把【论文摘要或正文】译成【英文/中文】，保持学术语体，专业术语首次出现时标注原文，附术语对照表。",
        blurb: "论文摘要、正文、审稿意见回复的中英学术互译。专业术语首次标注原文，按学术写作惯例调整句式，内置教育技术领域术语对照表。",
        skill: "translate",
      },
    ],
  },
  {
    id: "format", name: "格式校验", icon: "file-edit",
    items: [
      {
        id: "guokai-qianbaoformat",
        name: "签报文件格式校验（国开模版）",
        prompt: "帮我校验一下文件格式，看看哪些地方有问题，然后按格式修改",
        blurb: "按照国开总部的签报文件的格式模版对用户提交的文档进行格式校验以及格式应用。",
        skill: "guokai-qianbaoformat",
      },
      {
        id: "guokai-huiyijiyao-format",
        name: "会议纪要格式校验（国开模版）",
        prompt: "帮我校验一下文件格式，看看哪些地方有问题，然后按格式修改",
        blurb: "按照国开总部的会议纪要的格式模版对用户提交的文档进行格式校验以及格式应用。",
        skill: "guokai-huiyijiyao-format",
      },
      {
        id: "gongwenformat-pro",
        name: "公文排版（GB/T 9704）",
        prompt: "帮我把【文稿】排版成符合 GB/T 9704 的红头文件，输出可直接打印的 Word 文件。",
        blurb: "党政机关公文格式排版：把 Markdown / DOCX 转成符合 GB/T 9704-2012 的红头文件。",
        skill: "gongwenformat-pro",
      },
      {
        id: "cite-format",
        name: "引文格式转换",
        prompt: "帮我把【参考文献列表】从【APA/MLA】格式统一转换为【GB/T 7714】格式，信息缺失的条目请单独列出。",
        blurb: "参考文献格式规范化：APA／MLA／GB/T 7714 互转。自动识别文献类型，支持批量处理和从文件读入，附带命令行脚本可独立运行。",
        skill: "cite-format",
      },
      {
        id: "docx-editor-cn",
        name: "中文论文格式排版",
        prompt: "帮我把【论文稿】排版成符合发表论文格式规范的 Word 文件，包含封面、摘要、目录、正文、参考文献等。",
        blurb: "按中文论文格式规范排版：课程论文、数模竞赛论文、毕业论文，Markdown → docx，覆盖封面、摘要、目录、题注、公式编号、参考文献与页码分节。",
        skill: "docx-editor-cn",
      },
      {
        id: "humanizer-zh",
        name: "去AI味儿",
        prompt: "帮我把【AI 生成稿】改得像人写的，去掉空话和 AI 痕迹，补回具体细节与自然语气，保持原意不变。",
        blurb: "去除文本中的 AI 写作痕迹：夸大象征意义、宣传腔、模糊归因、三段式排比、AI 高频词、连接词过密等，并补回个性与语调，让内容自然可用。",
        skill: "humanizer-zh",
      },
    ],
  },
  {
    id: "slides", name: "幻灯片", icon: "file-ppt",
    items: [
      {
        id: "guokai-ppt",
        name: "本地pptx生成（国开模版）",
        prompt: "帮我基于【方案文档】生成一份国开模版的可编辑的 PPT，包含封面、目录、核心内容页、结语；输出可在 PowerPoint/WPS 直接编辑的 pptx 文件。",
        blurb: "从主题或大纲生成完整 PPTX，并按照国开总部的ppt的格式模版生成。",
        skill: "guokai-ppt",
      },
      {
        id: "ppt-master",
        name: "本地pptx生成",
        prompt: "帮我基于【方案文档】生成一份可编辑的 PPT，包含封面、目录、核心内容页、实施计划、结语；输出可在 PowerPoint/WPS 直接编辑的 pptx 文件。",
        blurb: "从主题或大纲生成完整 PPTX，含配色／版式／图标体系；另有「填充自有模板」的原生 OOXML 路径，不经 HTML 中转，产出真正可编辑的 pptx。",
        skill: "ppt-master",
      },
      {
        id: "frontend-slides",
        name: "HTML动画幻灯片",
        prompt: "帮我基于【课程内容或 PPTX】制作一套带动画的 HTML 幻灯片，输出单个 HTML 文件，可在浏览器直接播放。",
        blurb: "制作带动画的网页版演示文稿，或把现有 PPTX 转成网页演示。产出单个 HTML 文件、零依赖播放，可导出 PDF。",
        skill: "frontend-slides",
      },
    ],
  },
  {
    id: "teaching", name: "教育教学", icon: "book",
    items: [
      {
        id: "course-design",
        name: "在线课程设计",
        prompt: "帮我设计一门【XX课程】的在线课程方案，输出：课程目标、课程大纲、每单元的学习活动与资源清单、评估设计。",
        blurb: "KCP 三元模型（知识—能力—问题）＋五套教学法（Merrill／Gagné／UbD／知识森林／多媒体学习）的 7 阶段课程设计。含 5 套模板：KCP 模型、课程目标、课程大纲、单元设计、评估设计。",
        skill: "course-design",
      },
      {
        id: "tutor",
        name: "一对一自适应辅导",
        prompt: "帮我用一对一辅导的方式教我【XX知识点】：先探测我的基础水平，再按我的节奏讲解、出题检查、随时调整。",
        blurb: "PTCR 教学循环（计划→讲授→检查→反思）＋七套教学法按需切换。自动探测学习者水平、建立学习者画像、跨会话记忆学习进度，苏格拉底式提问引导。",
        skill: "tutor",
      },
      {
        id: "backwards-design-unit-planner",
        name: "逆向设计单元备课",
        prompt: "帮我按逆向设计规划【XX学科·XX单元】的单元备课：先确定期望的学习结果，再设计评估证据，最后倒推每课时的学习活动。",
        blurb: "按逆向设计（UbD）规划单元：从期望达成的学习结果 → 评估证据 → 学习活动。用于新单元设计或按课标重构旧单元。",
        skill: "backwards-design-unit-planner",
      },
      {
        id: "competency-unpacker",
        name: "课标能力点拆解",
        prompt: "帮我拆解课标条目【XX】，拆成可教可评的子技能与成功标准，给出可直接用于教案的学习目标表述。",
        blurb: "把宽泛的课标条目或素养描述拆成具体、可评价的成功标准与子技能。用于解读课程标准、撰写学习目标。",
        skill: "competency-unpacker",
      },
      {
        id: "practice-problem-sequence-designer",
        name: "分层练习题序列设计",
        prompt: "帮我为【XX知识点】设计一组分层练习题，从基础到变式到综合应用逐级递进，附答案与易错点提示。",
        blurb: "生成有梯度、带策略性变化的练习题序列。用于制作学案、作业单、独立练习材料。",
        skill: "practice-problem-sequence-designer",
      },
      {
        id: "differentiation-adapter",
        name: "分层教学任务改编",
        prompt: "帮我把【学案或任务】改编成三个版本：学困生版（增加支架与提示）、常规版、学优生版（提高开放度）。",
        blurb: "在保持核心学习目标不变的前提下，针对特定学习需求改编课堂任务。覆盖特殊教育需要、语言障碍、学优生、注意缺陷、阅读障碍、焦虑等情况。",
        skill: "differentiation-adapter",
      },
      {
        id: "teach",
        name: "交互式教学工作室",
        prompt: "帮我围绕【XX 主题】搭建一个系统完整的学习内容，按周为我规划整个学习旅程——拆解课程目标、排出课时节奏、记录每次课的学习进度。",
        blurb: "是一个完整的学习工作室。它像一位老师，为你规划使命、拆解课程、记录进度，每节课产出一个排版精良、可打印、随时回看的 HTML 课程页。",
        skill: "teach",
      },
    ],
  },
  {
    id: "thinking", name: "思维工具", icon: "sparkles",
    items: [
      {
        id: "think-challenge",
        name: "批判性思维",
        prompt: "帮我对【XX决策/方案】做一次压力测试：挑战隐含假设、指出逻辑弱点、提供替代方案、识别风险。",
        blurb: "决策前压力测试：挑战隐含假设、指出逻辑弱点、提供替代方案、识别风险。不附和用户，专门挑毛病，避免确认偏误。",
        skill: "think-challenge",
      },
      {
        id: "think-first-principles",
        name: "第一性原理解释",
        prompt: "帮我用第一性原理讲清【XX概念】：从最基础的概念逐层构建理解，不用类比、不跳步骤。",
        blurb: "从最基础概念逐层构建理解，不用类比不跳步骤：定义术语→逐层递进→展示连接→构建心智模型→实际应用→常见误解→要点总结。",
        skill: "think-first-principles",
      },
      {
        id: "brainstorming",
        name: "头脑风暴",
        prompt: "帮我就【XX主题】做一轮需求澄清与头脑风暴：先通过提问搞清我真正要解决的问题，再发散出多种方向和思路。",
        blurb: "在任何创造性工作开始前，先探索用户真实意图、需求和设计方向，通过提问澄清目标与成功标准，避免直接动手做错东西。",
        skill: "brainstorming",
      },
      {
        id: "think-prompt-optimizer",
        name: "提示词优化",
        prompt: "帮我优化这条提示词：【粘贴原提示词】，输出优化后的完整提示词并说明改了什么和为什么。",
        blurb: "系统性打磨 prompt：提升清晰度、结构、约束条件、输出格式、推理深度、具体性。输出优化后 prompt + 逐条改进说明 + 适用场景。",
        skill: "think-prompt-optimizer",
      },
      {
        id: "socratic-questioning",
        name: "苏格拉底式提问",
        prompt: "帮我用苏格拉底式提问，理清【我的困惑】，帮我找到真正值得回答的问题。",
        blurb: "通过苏格拉底式追问帮用户理清真正想问的问题。区分事实/解释/价值判断/目标，最多6个问题，最终给出可行动的新问题。",
        skill: "socratic-questioning",
      },
      {
        id: "dual-layer-explanation",
        name: "双层解释法",
        prompt: "帮我用双层解释法，学习【XX概念】，分别从小白和专业两个角度解释。",
        blurb: "小白版+专业版两层解释，帮助用户真正理解一个概念。避免似懂非懂的幻觉，附术语对应表+易错点+自检问题。",
        skill: "dual-layer-explanation",
      },
      {
        id: "minimum-viable-experiment",
        name: "最小实验",
        prompt: "帮我为【XX想法】设计一个最小实验，用行动代替空想。",
        blurb: "找出最需验证的假设，设计低成本、可逆、7天内可完成的最小实验。明确指标、成功/失败标准、明天就能开始的第一步。",
        skill: "minimum-viable-experiment",
      },
    ],
  },
  {
    id: "visual", name: "创意图文", icon: "file-image",
    items: [
      {
        id: "baoyu-diagram",
        name: "专业 SVG 图表",
        prompt: "帮我把【方案或描述】画成一张专业架构图/流程图，输出 SVG 文件。",
        blurb: "架构图、流程图、时序图、思维导图、时间线等各类结构／逻辑／流程可视化。内置统一设计系统，产出独立 .svg 文件。",
        skill: "baoyu-diagram",
      },
      {
        id: "canvas-design",
        name: "图片创作",
        prompt: "帮我为【XX活动】设计一张海报，包含主标题、核心卖点和联系方式，输出高分辨率图片。",
        blurb: "先确立设计哲学再动手排版，创作 PNG／PDF 视觉作品：海报、封面、艺术图，视觉物料质量高。",
        skill: "canvas-design",
      },
    ],
  },
  {
    id: "data", name: "数据分析", icon: "grid",
    items: [
      {
        id: "data-analysis",
        name: "表格数据分析",
        prompt: "帮我分析【Excel 数据】，给出总体分布、关键指标、对比与异常发现，配图表和改进建议。",
        blurb: "分析表格数据、产出洞察、做可视化、写公式与查询语句。纯 prompt 零依赖零联网，敏感数据不出本地。",
        skill: "data-analysis",
      },
      {
        id: "project-analysis-report",
        name: "多表格数据分析",
        prompt: "帮我基于【多个 Excel 报表】生成一份综合数据分析报告，包含关键指标、趋势图和洞察结论。",
        blurb: "从多个 Excel 自动生成综合 HTML 数据分析报告：KPI 卡片、Chart.js 图表、异常提示、AI 洞察，产出即演示级，移动端响应式。",
        skill: "project-analysis-report",
      },
      {
        id: "claude-d3js-skill",
        name: "专业数据可视化",
        prompt: "帮我把【数据】做成一个交互式可视化网页，输出单个 HTML 文件。",
        blurb: "使用 d3.js 创建专业级、可交互的定制数据可视化，表现力超出默认图表库，适合复杂关系与多维数据呈现。",
        skill: "claude-d3js-skill",
      },
    ],
  },
  {
    id: "integration", name: "软件联通", icon: "plug",
    items: [
      {
        id: "tencent-meeting-skill",
        name: "腾讯会议管理",
        prompt: "帮我在腾讯会议里预约一场【XX会议】，时间【下周三 14:00-15:30】，邀请【参会人名单】；会后取回 AI 智能纪要。",
        blurb: "腾讯会议管理：预约/创建/修改/取消会议、查询录制与转写、获取 AI 智能纪要",
        skill: "tencent-meeting-skill",
      },
      {
        id: "wecom-unified",
        name: "企微联通",
        prompt: "帮我把【通知】发到企业微信【XX群】，并给相关负责人建一条待办，截止日期【下周五】。",
        blurb: "企业微信 CLI 套件：文档/智能表格/消息/日程/会议/待办/通讯录",
        skill: "wecom-unified",
      },
      {
        id: "lark-unified",
        name: "飞书联通",
        prompt: "帮我把【会议纪要】存到飞书 Wiki 的【XX目录】，把待办拆成任务分派给对应责任人，并在群里发一条摘要。",
        blurb: "飞书/Lark 全能套件：消息、文档、表格、日历、任务、Wiki 等 11 个业务域",
        skill: "lark-unified",
      },
    ],
  },
]

/**
 * skillHint 是「先用这个技能」那句话，拼在场景提示词前面。
 *
 * 为什么要显式点名，而不是靠引擎自己判断：引擎按 frontmatter 的 description 决定
 * 加不加载某个技能，那是**面向全部技能的一次匹配**，装了几十个之后未必挑中你想要
 * 的那一个。而场景与技能的对应关系是产品侧一条条定死的——已经知道答案了，就别让
 * 模型再猜一次。
 *
 * 只在技能**确实可用**时才拼（见 App 的 pickScenario）：点名一个不存在的技能，
 * 模型会去调 Skill 工具然后报一句找不到，比不提它还糟。
 */
export function skillHint(skill: string): string {
  return skill ? `请使用「${skill}」技能完成以下任务：
` : ''
}

/**
 * BUILTIN_CATEGORY 把分类映射到「它其实是客户端的一个内置功能」。
 *
 * 录音纪要就一条，而且不是提示词——录音是点一下就该开始的动作，让用户去输入框里
 * 打字触发不合适（这也是它原先单独摆在这排里的原因）。所以这个分类不展开二级面板，
 * 点了直接执行，由 App 把动作接进来。
 */
export const BUILTIN_CATEGORY: Record<string, string> = { recorder: 'recorder' }

/**
 * firstPlaceholder 找出提示词里第一个【占位符】的位置（含书名号本身）。
 *
 * 点了场景要把提示词填进输入框并**选中**这一段，用户接着打字就替换掉它。直接发送
 * 是不行的：模型收到「帮我调研【XX话题】」只能反问，而那一问一答本可以省掉。
 *
 * 返回 null 表示这条提示词没有占位符（表里有 7 条是这样，比如「帮我做人生设计」），
 * 调用方把光标放到末尾即可。
 */
export function firstPlaceholder(prompt: string): { start: number; end: number } | null {
  const start = prompt.indexOf('\u3010')
  if (start < 0) return null
  const end = prompt.indexOf('\u3011', start)
  if (end < 0) return null
  return { start, end: end + 1 }
}

/**
 * applyScenario 算出「点了这条场景之后输入框该是什么、选中哪一段」。
 *
 * 输入框空着就整段替换；已经有字了则**另起一段追加**，不覆盖用户已经打的内容——
 * 场景提示词是一整段模板，直接顶掉别人写了一半的话，损失比省下的一次点击大得多。
 */
export function applyScenario(current: string, prompt: string): { value: string; start: number; end: number } {
  const prefix = current.trim() ? current.replace(/\s+$/, '') + '\n\n' : ''
  const value = prefix + prompt
  const ph = firstPlaceholder(prompt)
  return ph
    ? { value, start: prefix.length + ph.start, end: prefix.length + ph.end }
    : { value, start: value.length, end: value.length }
}
