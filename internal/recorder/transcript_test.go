package recorder

import (
	"strings"
	"testing"
)

func ev(t string, track Source, seg, rev int, text string, bt int64) Event {
	return Event{Type: t, Track: string(track), Seg: seg, Rev: rev, Text: text, BT: bt}
}

func fold(events ...Event) *Transcript {
	tx := NewTranscript()
	for _, e := range events {
		tx.Apply(e)
	}
	return tx
}

func texts(segs []Segment) []string {
	out := make([]string, len(segs))
	for i, s := range segs {
		out[i] = s.Text
	}
	return out
}

func TestTranscriptOnlyKeepsFinals(t *testing.T) {
	// 实时行随时会被确认文本顶掉，进最终稿就会出现「同一句说了两遍」。
	tx := fold(
		ev("partial", SourceMic, LiveSeg, RevPartial, "这个方案", 0),
		ev("final", SourceMic, 0, RevFinal, "这个方案先上灰度", 0),
		ev("partial", SourceMic, LiveSeg, RevPartial, "下一步", 5000),
	)
	if got := texts(tx.Segments()); len(got) != 1 || got[0] != "这个方案先上灰度" {
		t.Fatalf("最终稿不对：%v", got)
	}
}

func TestTranscriptInterleavesTracksByTimeline(t *testing.T) {
	// 上行是两条独立的 WebSocket，回环轨的事件完全可能比更早说的麦克风轨先到。
	tx := fold(
		ev("final", SourceLoopback, 0, RevFinal, "第二句", 5000),
		ev("final", SourceMic, 0, RevFinal, "第一句", 1000),
		ev("final", SourceMic, 1, RevFinal, "第三句", 9000),
	)
	want := []string{"第一句", "第二句", "第三句"}
	got := texts(tx.Segments())
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("排序不对：%v", got)
		}
	}
}

func TestTranscriptDropThenReissue(t *testing.T) {
	// 修订：段落边界变了（两句合并成一句），旧段整批撤掉再发新的。
	tx := fold(
		ev("final", SourceMic, 0, RevFinal, "这个方案", 0),
		ev("final", SourceMic, 1, RevFinal, "先上灰度", 1000),
	)
	tx.Apply(Event{Type: "drop", Track: string(SourceMic), Segs: []int{0, 1}})
	if got := tx.Segments(); len(got) != 0 {
		t.Fatalf("drop 之后还剩 %d 段", len(got))
	}
	tx.Apply(ev("final", SourceMic, 2, RevFinal, "这个方案先上灰度", 0))
	if got := texts(tx.Segments()); len(got) != 1 || got[0] != "这个方案先上灰度" {
		t.Fatalf("重发后不对：%v", got)
	}
}

func TestTranscriptDropDoesNotHitOtherTrack(t *testing.T) {
	// seg 是每轨各自递增的，两条轨的 seg 0 是两个不同的句子。
	tx := fold(
		ev("final", SourceMic, 0, RevFinal, "我说的", 0),
		ev("final", SourceLoopback, 0, RevFinal, "对方说的", 100),
	)
	tx.Apply(Event{Type: "drop", Track: string(SourceMic), Segs: []int{0}})
	if got := texts(tx.Segments()); len(got) != 1 || got[0] != "对方说的" {
		t.Fatalf("drop 误伤了另一条轨：%v", got)
	}
}

func TestTranscriptLowerRevDoesNotOverwrite(t *testing.T) {
	// 精修（rev 2/3）晚到是常态，但断线重连后服务端会重放，低 rev 可能后到——
	// 照单全收就会把精修好的文本又换回粗结果。
	tx := fold(ev("final", SourceMic, 0, RevL3, "精修后的文本", 0))
	tx.Apply(ev("final", SourceMic, 0, RevFinal, "粗结果", 0))
	if got := texts(tx.Segments()); got[0] != "精修后的文本" {
		t.Fatalf("被低 rev 覆盖了：%v", got)
	}
}

func TestTranscriptRespeaker(t *testing.T) {
	tx := NewTranscript()
	tx.Apply(Event{Type: "final", Track: string(SourceLoopback), Seg: 0, Rev: RevFinal, Text: "我同意", Spk: "spk1"})
	if tx.Segments()[0].Speaker != "spk1" {
		t.Fatalf("初始说话人不对：%+v", tx.Segments()[0])
	}
	tx.Apply(Event{Type: "respeaker", Track: string(SourceLoopback), Seg: 0, Spk: "spk1", Name: "马文涛"})
	s := tx.Segments()[0]
	if s.Speaker != "马文涛" || s.Text != "我同意" {
		t.Fatalf("追认说话人后不对：%+v", s)
	}
}

func TestTranscriptSpeakerFallsBackToTrackLabel(t *testing.T) {
	// 没有姓名也没有聚类编号时退到轨道称呼——「我」和「对方」是双轨方案唯一
	// 暴露给用户的概念。
	tx := fold(
		ev("final", SourceMic, 0, RevFinal, "a", 0),
		ev("final", SourceLoopback, 0, RevFinal, "b", 1),
	)
	got := tx.Segments()
	if got[0].Speaker != "我" || got[1].Speaker != "对方" {
		t.Fatalf("轨道称呼不对：%q / %q", got[0].Speaker, got[1].Speaker)
	}
}

func TestTranscriptMarkdown(t *testing.T) {
	tx := fold(
		ev("final", SourceMic, 0, RevFinal, "先说结论", 65_000),
		ev("final", SourceLoopback, 0, RevFinal, "同意", 3_725_000),
	)
	md := tx.Markdown("季度评审")
	for _, want := range []string{"# 季度评审", "[01:05] 我", "先说结论", "[1:02:05] 对方", "同意"} {
		if !strings.Contains(md, want) {
			t.Fatalf("Markdown 里缺 %q：\n%s", want, md)
		}
	}
}

func TestTranscriptMarkdownEmpty(t *testing.T) {
	if md := NewTranscript().Markdown("空场"); !strings.Contains(md, "没有转写文本") {
		t.Fatalf("空转写应当说明白：%q", md)
	}
}
