package repl

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wt68/runcode/engine/tool"
)

// ReasoningExecMode decides where a scenario's structured analysis runs: a
// dedicated pre-turn pass (heavier scenarios that benefit from framing the whole
// approach up front) or in-turn (lighter scenarios that need to investigate first
// — handled by the forced-analysis tool).
type ReasoningExecMode string

const (
	ReasoningExecPreTurn ReasoningExecMode = "pre_turn"
	ReasoningExecInTurn  ReasoningExecMode = "in_turn"
)

// ReasoningStep is one mandatory step of a scenario's thinking protocol. The Key
// is the JSON field the model must fill; Label/Hint describe what goes in it.
type ReasoningStep struct {
	Key   string
	Label string
	Hint  string
}

// ReasoningProtocol is a scenario's hardened thinking process: an ordered set of
// steps the model must fill, plus where it runs. It replaces the old free-text
// checklist with a code-defined, enforced structure.
type ReasoningProtocol struct {
	Scenario ReasoningScenario
	Mode     ReasoningExecMode
	Method   string
	Steps    []ReasoningStep
}

var reasoningProtocols = map[ReasoningScenario]ReasoningProtocol{
	ReasoningScenarioTroubleshooting: {
		Scenario: ReasoningScenarioTroubleshooting, Mode: ReasoningExecInTurn,
		Method: "5 Whys + 假设验证 + 奥卡姆剃刀",
		Steps: []ReasoningStep{
			{"symptom", "现象与范围", "具体症状、复现条件、影响范围"},
			{"hypotheses", "可能假设", "按可能性排序的原因假设"},
			{"validation", "验证方式", "如何验证或排除每个假设"},
			{"root_cause", "最可能根因", "当前最可能的根本原因及依据"},
			{"fix", "修复方案", "针对根因的修复步骤"},
			{"verification", "验证方法", "如何确认修复有效且无回归"},
		},
	},
	ReasoningScenarioArchitecture: {
		Scenario: ReasoningScenarioArchitecture, Mode: ReasoningExecPreTurn,
		Method: "第一性原理 + 系统思维 + 逆向",
		Steps: []ReasoningStep{
			{"goal", "目标", "要解决的核心问题与成功标准"},
			{"constraints", "约束", "技术、资源、兼容性等硬约束"},
			{"options", "候选方案", "2-3 个可行架构方案概述"},
			{"tradeoffs", "权衡对比", "各方案在可靠性、扩展性、复杂度、成本上的取舍"},
			{"recommendation", "推荐", "推荐方案及理由"},
			{"risks", "风险", "主要风险与缓解措施"},
		},
	},
	ReasoningScenarioProposal: {
		Scenario: ReasoningScenarioProposal, Mode: ReasoningExecPreTurn,
		Method: "金字塔原理 + MECE + 成本收益",
		Steps: []ReasoningStep{
			{"problem", "问题", "要解决的问题与背景"},
			{"goal", "目标", "期望达成的结果"},
			{"options", "候选", "互斥且穷尽的可选方案"},
			{"cost_benefit", "成本收益", "各方案的成本、收益、风险"},
			{"recommendation", "推荐", "推荐方案及理由"},
			{"next_steps", "下一步", "落地步骤与优先级"},
		},
	},
	ReasoningScenarioProject: {
		Scenario: ReasoningScenarioProject, Mode: ReasoningExecInTurn,
		Method: "闭环思维 + 80/20",
		Steps: []ReasoningStep{
			{"objective", "目标", "交付目标与验收标准"},
			{"breakdown", "拆解", "关键任务拆解"},
			{"priorities", "优先级", "按 80/20 的关键路径排序"},
			{"risks", "风险", "阻塞项与依赖"},
			{"plan", "计划", "阶段与里程碑"},
		},
	},
	ReasoningScenarioIncident: {
		Scenario: ReasoningScenarioIncident, Mode: ReasoningExecInTurn,
		Method: "OODA + 假设验证",
		Steps: []ReasoningStep{
			{"observe", "观察", "当前现象与影响面"},
			{"orient", "研判", "最可能的方向与依据"},
			{"mitigate", "止血", "立即可行的止血措施"},
			{"locate", "定位", "定位根因的下一步"},
			{"recover", "恢复与复盘", "恢复方案与后续复盘项"},
		},
	},
	ReasoningScenarioGeneral: {
		Scenario: ReasoningScenarioGeneral, Mode: ReasoningExecInTurn,
		Method: "通用结构化分析",
		Steps: []ReasoningStep{
			{"problem", "问题", "要做什么、目标是什么"},
			{"facts", "已知与未知", "已知事实与待确认假设"},
			{"approach", "方案", "打算怎么做"},
			{"verification", "验证", "如何确认完成且正确"},
		},
	},
}

// protocolFor returns the thinking protocol for a scenario.
func protocolFor(scenario ReasoningScenario) (ReasoningProtocol, bool) {
	p, ok := reasoningProtocols[scenario]
	return p, ok
}

// analysisSystemPrompt builds the instruction for the pre-turn structured pass:
// fill every step as JSON, with concrete, task-specific content.
func (p ReasoningProtocol) analysisSystemPrompt() string {
	var b strings.Builder
	fmt.Fprintf(&b, "你正在用「%s」方法对用户任务做结构化分析。严格按以下步骤逐项分析。\n", p.Method)
	b.WriteString("只输出一个 JSON 对象,每个字段填写针对本任务的具体内容(中文、简洁、有据),不得留空、不得照抄提示语:\n")
	for i, step := range p.Steps {
		fmt.Fprintf(&b, "%d. %s(%s)→ 字段 \"%s\"\n", i+1, step.Label, step.Hint, step.Key)
	}
	b.WriteString("\n输出格式示例:{")
	for i, step := range p.Steps {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "\"%s\": \"...\"", step.Key)
	}
	b.WriteString("}")
	return b.String()
}

// parseStructuredAnalysis extracts the filled step map from the model's JSON
// reply. It errors if the JSON is unparseable or every step is empty.
func (p ReasoningProtocol) parseStructuredAnalysis(text string) (map[string]string, error) {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("%w: no json object", ErrInvalidReasoningClassification)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(text[start:end+1]), &raw); err != nil {
		return nil, fmt.Errorf("%w: parse analysis json: %v", ErrInvalidReasoningClassification, err)
	}
	filled := make(map[string]string, len(p.Steps))
	any := false
	for _, step := range p.Steps {
		value := strings.TrimSpace(fmt.Sprintf("%v", raw[step.Key]))
		if value == "<nil>" {
			value = ""
		}
		filled[step.Key] = value
		if value != "" {
			any = true
		}
	}
	if !any {
		return nil, fmt.Errorf("%w: empty analysis", ErrInvalidReasoningClassification)
	}
	return filled, nil
}

// analysisInput renders a filled analysis as the Analyze tool's input shape, so the
// pre-turn pass surfaces as the same UI card as the in-turn Analyze tool. It carries
// the method and each step's human label so the UI can render a fully-labeled
// structured-thinking card without duplicating the protocol.
func (p ReasoningProtocol) analysisInput(filled map[string]string) json.RawMessage {
	b, _ := p.analysisInputFrom(filled)
	return b
}

// enrichAnalysisInput rewrites a model-produced Analyze input ({steps:[{key,content}]})
// into the same labeled, method-tagged shape as analysisInput, re-ordered to the
// protocol's canonical step order (the model may fill them out of order). It is used
// to make an in-turn Analyze call render the same rich card as the pre-turn pass.
// Returns the original bytes unchanged if they cannot be parsed.
func (p ReasoningProtocol) enrichAnalysisInput(raw []byte) json.RawMessage {
	var in struct {
		Steps []struct {
			Key     string `json:"key"`
			Content string `json:"content"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return raw
	}
	content := make(map[string]string, len(in.Steps))
	for _, s := range in.Steps {
		content[s.Key] = s.Content
	}
	b, _ := p.analysisInputFrom(content)
	if b == nil {
		return raw
	}
	return b
}

// analysisOutputLines renders a filled analysis as display lines for the UI card.
func (p ReasoningProtocol) analysisOutputLines(filled map[string]string) []tool.OutputLine {
	lines := make([]tool.OutputLine, 0, len(p.Steps))
	for _, step := range p.Steps {
		value := filled[step.Key]
		if value == "" {
			value = "(未给出)"
		}
		lines = append(lines, tool.OutputLine{Text: fmt.Sprintf("%s:%s", step.Label, value)})
	}
	return lines
}

// inTurnInstruction tells the model to complete the protocol via the Analyze tool
// before doing anything else. It is injected for in-turn scenarios.
func (p ReasoningProtocol) inTurnInstruction() string {
	var b strings.Builder
	fmt.Fprintf(&b, "本回合启用「%s」结构化思考流程。你必须先调用 Analyze 工具,逐步给出分析(每步一个 {key, content},内容要具体、针对本任务、不照抄提示),完成后才能使用其它工具或作答。\n步骤:\n", p.Method)
	for i, step := range p.Steps {
		fmt.Fprintf(&b, "%d. %s(%s)→ key=\"%s\"\n", i+1, step.Label, step.Hint, step.Key)
	}
	return strings.TrimRight(b.String(), "\n")
}

// missingAnalysisSteps parses an Analyze tool call's input and returns the labels
// of protocol steps left missing or empty.
func (p ReasoningProtocol) missingAnalysisSteps(raw []byte) []string {
	var in struct {
		Steps []struct {
			Key     string `json:"key"`
			Content string `json:"content"`
		} `json:"steps"`
	}
	_ = json.Unmarshal(raw, &in)
	filled := make(map[string]string, len(in.Steps))
	for _, s := range in.Steps {
		filled[s.Key] = strings.TrimSpace(s.Content)
	}
	var missing []string
	for _, step := range p.Steps {
		if filled[step.Key] == "" {
			missing = append(missing, step.Label)
		}
	}
	return missing
}

// gatePromptMessage is the tool result returned when the model tries another tool
// before completing the analysis.
func (p ReasoningProtocol) gatePromptMessage() string {
	keys := make([]string, len(p.Steps))
	for i, step := range p.Steps {
		keys[i] = step.Key
	}
	return fmt.Sprintf("结构化思考流程已启用(%s):请先调用 Analyze 工具完成分析(步骤 key:%s),再使用其它工具。", p.Method, strings.Join(keys, "、"))
}

// renderAnalysis turns a filled step map into the grounding block injected into
// the main turn, so the answer builds on the completed analysis.
func (p ReasoningProtocol) renderAnalysis(filled map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "你已用「%s」完成如下结构化分析,请据此作答与行动,不要重复推导:\n", p.Method)
	for i, step := range p.Steps {
		value := filled[step.Key]
		if value == "" {
			value = "(未给出)"
		}
		fmt.Fprintf(&b, "%d. %s:%s\n", i+1, step.Label, value)
	}
	return strings.TrimRight(b.String(), "\n")
}
