package ui

// 异步动作的 tea.Cmd 工厂。都遵循同一个约定:命令本身立刻返回,真正的工作在
// goroutine 或阻塞读里进行,完成后经消息回到 Update——所以渲染线程从不被阻塞。
// (与 slash_commands.go 的"斜杠命令"是两回事,别混。)

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
)

// waitEventCmd 阻塞读一条事件;Update 处理完后再排一个,形成事件泵。
func waitEventCmd(events <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-events
	}
}

// runTurnCmd 起一个回合。它自己不等结果——回合在独立 goroutine 里跑，完成或
// 失败都经 events 送回,这样取消(ctx)与流式增量能并发进行。
func runTurnCmd(ctx context.Context, service Service, text string, events chan<- tea.Msg) tea.Cmd {
	return func() tea.Msg {
		go func() {
			result, err := service.RunTurn(ctx, text)
			if err != nil {
				events <- turnErrorMsg{Err: err}
				return
			}
			events <- turnDoneMsg{Result: result}
		}()
		return nil
	}
}

func resetCmd(service Service) tea.Cmd {
	return func() tea.Msg {
		if err := service.Reset(context.Background()); err != nil {
			return resetErrorMsg{Err: err}
		}
		return resetDoneMsg{}
	}
}

func compactCmd(service Service) tea.Cmd {
	return func() tea.Msg {
		result, err := service.Compact(context.Background())
		if err != nil {
			return compactErrorMsg{Err: err}
		}
		return compactDoneMsg{Result: result}
	}
}
