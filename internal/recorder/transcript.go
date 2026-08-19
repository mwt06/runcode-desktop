package recorder

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Transcript 把网关下行的事件流折叠成一份可落盘的转写。
//
// 它是这场录音唯一的持久文本。界面那份（frontend/src/recorder/transcript.ts）是
// 同一套规则的另一实现，但只活在 WebView 的内存里——刷新一下就没了，更不可能
// 拿去生成纪要。两份实现刻意保持一致，改规则时两边都要改；之所以不做成一处，是
// 因为界面要的是每来一条事件就增量重渲染，而这边要的是收尾时一次性成文，把它们
// 硬凑到一起只会让两边都别扭。
//
// 折叠规则（engine/track.py）：
//
//	partial     实时行，不进最终稿——它随时会被确认文本顶掉。
//	final       确认段。同 seg 重发时按 rev 取高的：精修（rev 2/3）可能比确认
//	            文本晚到，也可能因为重连而乱序到达。
//	drop        撤掉一批 seg。修订时段落边界会变，没法一一对应地改，只能整批
//	            撤掉再发新的。
//	respeaker   重聚类改了历史段落的说话人，按 seg 追认。
type Transcript struct {
	mu   sync.Mutex
	segs map[string]*Segment
}

// Segment 是最终稿里的一句话。
type Segment struct {
	Track   Source `json:"track"`
	Seg     int    `json:"seg"`
	Rev     int    `json:"rev"`
	Text    string `json:"text"`
	BT      int64  `json:"bt"`
	ET      int64  `json:"et"`
	Speaker string `json:"speaker"`
}

// NewTranscript 建一份空转写。
func NewTranscript() *Transcript {
	return &Transcript{segs: map[string]*Segment{}}
}

func segKey(track Source, seg int) string {
	return string(track) + ":" + fmt.Sprint(seg)
}

// speakerOf 优先用声纹库认出的姓名，退到盲聚类编号，最后退到轨道称呼。
func speakerOf(ev Event) string {
	if ev.Name != "" {
		return ev.Name
	}
	if ev.Spk != "" {
		return ev.Spk
	}
	return TrackLabel(Source(ev.Track))
}

// TrackLabel 把音轨换成人能读的称呼。这是双轨方案唯一暴露给用户的地方：
// 麦克风是「我」，系统回环是会议软件里对方的声音。
func TrackLabel(src Source) string {
	switch src {
	case SourceMic:
		return "我"
	case SourceLoopback:
		return "对方"
	default:
		return string(src)
	}
}

// Apply 折进一条事件。它在上行的读 goroutine 上被调用，必须快。
func (t *Transcript) Apply(ev Event) {
	track := Source(ev.Track)

	t.mu.Lock()
	defer t.mu.Unlock()

	switch ev.Type {
	case "final":
		key := segKey(track, ev.Seg)
		// 低 rev 不能覆盖高 rev —— 否则重连时服务端重放会把精修好的文本
		// 又换回粗结果。
		if old, ok := t.segs[key]; ok && old.Rev > ev.Rev {
			return
		}
		t.segs[key] = &Segment{
			Track: track, Seg: ev.Seg, Rev: ev.Rev, Text: ev.Text,
			BT: ev.BT, ET: ev.ET, Speaker: speakerOf(ev),
		}

	case "drop":
		for _, s := range ev.Segs {
			delete(t.segs, segKey(track, s))
		}

	case "respeaker":
		if s, ok := t.segs[segKey(track, ev.Seg)]; ok {
			s.Speaker = speakerOf(ev)
		}
	}
	// partial / live_clear / live_status / ready 都不进最终稿。
}

// Segments 返回按房间时间轴排好序的最终稿。
func (t *Transcript) Segments() []Segment {
	t.mu.Lock()
	defer t.mu.Unlock()

	out := make([]Segment, 0, len(t.segs))
	for _, s := range t.segs {
		out = append(out, *s)
	}
	// bt 相同时（两人同时开口）用轨道再用段号定序，保证同一批事件无论到达顺序
	// 如何，落盘的文件都是同一个样子。
	sort.Slice(out, func(i, j int) bool {
		if out[i].BT != out[j].BT {
			return out[i].BT < out[j].BT
		}
		if out[i].Track != out[j].Track {
			return out[i].Track < out[j].Track
		}
		return out[i].Seg < out[j].Seg
	})
	return out
}

// Markdown 把转写渲染成带时间戳与说话人的文本，交给模型生成纪要，也直接给人看。
//
// 用 Markdown 而不是 JSON：这份文件既是程序的输入也是人的阅读材料（设计稿里
// 「原始记录」那一栏就是它），而模型读带时间戳的对话稿也比读一堆字段更稳。
func (t *Transcript) Markdown(title string) string {
	segs := t.Segments()

	var b strings.Builder
	if title != "" {
		b.WriteString("# " + title + "\n\n")
	}
	if len(segs) == 0 {
		b.WriteString("_这场录音没有转写文本。_\n")
		return b.String()
	}
	for _, s := range segs {
		fmt.Fprintf(&b, "**[%s] %s**：%s\n\n", stamp(s.BT), s.Speaker, s.Text)
	}
	return b.String()
}

// stamp 把房间时间轴上的毫秒格成 mm:ss（超过一小时补出小时位）。
func stamp(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	total := ms / 1000
	h, m, s := total/3600, total%3600/60, total%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}
