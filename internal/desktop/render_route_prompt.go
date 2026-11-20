package desktop

import engine "gitlab.ouc-online.com.cn/aibase/agentloop"

// 客户端负责安全地显示结果，生成侧必须按任务语义选格式；
// 这里只声明格式分工，不复制工具执行和权限判断。
const renderRoutePrompt = `Content rendering:
- Follow an explicit output format requested by the user, while distinguishing a request to create that format from text that merely discusses it.
- For flowcharts, architecture diagrams, sequence diagrams, state diagrams, ER diagrams, gantt charts, and mind maps, output a Mermaid fenced code block directly. Do not create an HTML file only to preview Mermaid.
- For precise vector graphics, geometry, technical illustrations, and static data charts, prefer SVG when SVG is a suitable output.
- For web pages, interactive interfaces, complex layouts, and dashboards, use HTML. The desktop chat preview is static and does not run model-generated scripts.
`

// 生图路由只在当前会话注册 image_generate 时追加，避免普通自定义模型与 OA
// 会话携带不存在的工具说明。
const imageGenerationRoutePrompt = `Image generation routing:
- Use image_generate only when the main requirement depends on realistic imagery, people, photography, complex textures, or artistic illustration. Do not use image generation for ordinary flowcharts, statistical charts, or diagrams whose text and geometry must be exact.
- If the user asked for image_generate-class imagery (photos, posters, product shots, artistic illustrations of objects/people/scenes) and image_generate is unavailable or fails, report that generation failed. Do not substitute SVG, Mermaid, or HTML drawings for that request. Tell the user they can click 「修改生图设置」 at the end of the reply to open image-generation settings.`

func appendRenderRoutePrompts(cfg *engine.Config, imageGeneration bool) {
	appendSystemPrompt(cfg, renderRoutePrompt)
	if imageGeneration {
		appendSystemPrompt(cfg, imageGenerationRoutePrompt)
	}
}
