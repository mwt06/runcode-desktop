package recorder

import "testing"

// collect 把 emit 出来的帧拷贝下来（emit 给的切片会失效，直接存会全指向同一块）。
func collect(frames *[][]int16) func([]int16) {
	return func(f []int16) {
		*frames = append(*frames, append([]int16(nil), f...))
	}
}

func TestFramerEmitsFixedSize(t *testing.T) {
	f := NewFramer(4)
	var got [][]int16
	f.Push([]int16{1, 2, 3, 4, 5, 6, 7, 8, 9}, collect(&got))

	if len(got) != 2 {
		t.Fatalf("9 个样本按 4 分帧应出 2 帧，得到 %d", len(got))
	}
	want := [][]int16{{1, 2, 3, 4}, {5, 6, 7, 8}}
	for i := range want {
		for j := range want[i] {
			if got[i][j] != want[i][j] {
				t.Fatalf("第 %d 帧第 %d 个：%d != %d", i, j, got[i][j], want[i][j])
			}
		}
	}
	if f.Pending() != 1 {
		t.Errorf("应余下 1 个样本，实际 %d", f.Pending())
	}
}

// TestFramerIgnoresInputChunking 是这一层存在的理由：驱动给的缓冲大小会抖，
// 分帧结果必须与它无关。
func TestFramerIgnoresInputChunking(t *testing.T) {
	in := make([]int16, 1000)
	for i := range in {
		in[i] = int16(i)
	}

	var want [][]int16
	one := NewFramer(96)
	one.Push(in, collect(&want))

	for _, chunk := range []int{1, 5, 96, 97, 333, 999} {
		var got [][]int16
		f := NewFramer(96)
		for i := 0; i < len(in); i += chunk {
			f.Push(in[i:min(i+chunk, len(in))], collect(&got))
		}
		if len(got) != len(want) {
			t.Fatalf("块长 %d：%d 帧 != %d 帧", chunk, len(got), len(want))
		}
		for i := range want {
			for j := range want[i] {
				if got[i][j] != want[i][j] {
					t.Fatalf("块长 %d：第 %d 帧第 %d 个 %d != %d",
						chunk, i, j, got[i][j], want[i][j])
				}
			}
		}
	}
}

// TestFramerDrainKeepsTail 守「最后一句没了」这个故障。
func TestFramerDrainKeepsTail(t *testing.T) {
	f := NewFramer(4)
	var got [][]int16
	f.Push([]int16{1, 2, 3, 4, 5, 6}, collect(&got))
	f.Drain(collect(&got))

	if len(got) != 2 {
		t.Fatalf("应有 1 个整帧 + 1 个尾帧，得到 %d 帧", len(got))
	}
	if len(got[1]) != 2 || got[1][0] != 5 || got[1][1] != 6 {
		t.Errorf("尾帧应为 [5 6]，得到 %v", got[1])
	}
	// 不补静音填满——补出来的是假音频。
	if len(got[1]) == 4 {
		t.Error("尾帧被补齐成整帧了，不应该")
	}
	if f.Pending() != 0 {
		t.Errorf("Drain 后应清空，仍余 %d", f.Pending())
	}

	// 空的时候 Drain 不应发出空帧。
	var empty [][]int16
	NewFramer(4).Drain(collect(&empty))
	if len(empty) != 0 {
		t.Errorf("空分帧器 Drain 不应产出帧，得到 %d", len(empty))
	}
}

func TestIsSilent(t *testing.T) {
	zeros := make([]int16, 100)
	if !IsSilent(zeros) {
		t.Error("全零应判静音（回环轨没播放时就是这样）")
	}

	// 底噪级别仍算静音。
	noise := make([]int16, 100)
	for i := range noise {
		noise[i] = int16(i%5) - 2
	}
	if !IsSilent(noise) {
		t.Error("±2 的底噪应判静音")
	}

	// 一个明显的样本就足以判为有声——宁可多传，不能切掉气声。
	voice := make([]int16, 100)
	voice[42] = 500
	if IsSilent(voice) {
		t.Error("含 500 幅度样本的帧不应判静音")
	}

	// 负向同样要认。
	neg := make([]int16, 100)
	neg[7] = -500
	if IsSilent(neg) {
		t.Error("负向越阈同样应判为有声")
	}
}
