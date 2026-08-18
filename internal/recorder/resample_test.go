package recorder

import (
	"math"
	"testing"
)

// tone 造一段幅度 1.0 的正弦。
func tone(freq, rate float64, n int) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(math.Sin(2 * math.Pi * freq * float64(i) / rate))
	}
	return out
}

// amplitudeAt 用直接相关测某个频率分量的幅度（单频信号下比 FFT 更直白）。
func amplitudeAt(x []float32, freq, rate float64) float64 {
	var re, im float64
	for i, v := range x {
		t := 2 * math.Pi * freq * float64(i) / rate
		re += float64(v) * math.Cos(t)
		im -= float64(v) * math.Sin(t)
	}
	return 2 * math.Hypot(re, im) / float64(len(x))
}

func rms(x []float32) float64 {
	var acc float64
	for _, v := range x {
		acc += float64(v) * float64(v)
	}
	return math.Sqrt(acc / float64(len(x)))
}

// naiveDecimate 是「每 m 个取 1 个」，本项目**明确不用**的做法。
// 只在 TestNoAliasing 里当反面对照，证明那个坑是真的。
func naiveDecimate(x []float32, m int) []float32 {
	out := make([]float32, 0, len(x)/m)
	for i := 0; i < len(x); i += m {
		out = append(out, x[i])
	}
	return out
}

func TestPassthroughWhenRatesMatch(t *testing.T) {
	r := NewResampler(16000, 16000)
	if l, m := r.Ratio(); l != 1 || m != 1 {
		t.Fatalf("16k→16k 应该是 1:1，得到 %d:%d", l, m)
	}
	in := tone(1000, 16000, 4800)
	out := r.Process(in)
	if len(out) != len(in) {
		t.Fatalf("直通应原样返回，长度 %d != %d", len(out), len(in))
	}
	for i := range in {
		if out[i] != in[i] {
			t.Fatalf("直通在第 %d 个样本被改动了", i)
		}
	}
}

// TestNoAliasing 是这个包存在的理由。
//
// 48 kHz 的 10 kHz 音，抽取 3 倍到 16 kHz。10 kHz 超过输出奈奎斯特 8 kHz，
// 朴素抽取会把它折叠到 |10000−16000| = 6000 Hz，幅度几乎不衰减——听起来像是
// 凭空多出一个哨音，而且落在语音频段正中间。
func TestNoAliasing(t *testing.T) {
	const (
		inRate  = 48000.0
		outRate = 16000.0
		freq    = 10000.0
		alias   = 6000.0 // |freq − outRate|
	)
	in := tone(freq, inRate, 48000) // 1 秒

	// 反面对照：先证明这个坑真的存在。
	bad := naiveDecimate(in, 3)
	badAmp := amplitudeAt(bad, alias, outRate)
	if badAmp < 0.5 {
		t.Fatalf("对照组没能复现混叠（%.3f），说明测试本身有问题", badAmp)
	}
	t.Logf("朴素抽取：6 kHz 处出现幅度 %.3f 的鬼影", badAmp)

	// 正式路径。
	r := NewResampler(inRate, outRate)
	out := r.Process(in)
	// 掐掉滤波器预热的头尾，只看稳态。
	warm := r.TapsPerPhase()
	if len(out) <= 2*warm {
		t.Fatalf("输出太短（%d）没法掐头尾", len(out))
	}
	steady := out[warm : len(out)-warm]

	goodAmp := amplitudeAt(steady, alias, outRate)
	att := 20 * math.Log10(badAmp/math.Max(goodAmp, 1e-12))
	t.Logf("带限抽取：6 kHz 处幅度 %.6f（相对对照组衰减 %.1f dB），抽头 %d",
		goodAmp, att, r.TapsPerPhase())
	if att < 50 {
		t.Errorf("混叠抑制只有 %.1f dB，要求 ≥ 50 dB", att)
	}

	// 全带 RMS 也应该塌下去——不只是 6 kHz 那一根，是整段能量都没了。
	if got := rms(steady); got > 0.01 {
		t.Errorf("稳态 RMS %.5f 偏高，10 kHz 的能量没被压干净", got)
	}
}

func TestPassbandPreserved(t *testing.T) {
	const inRate, outRate = 48000.0, 16000.0
	for _, freq := range []float64{100, 500, 1000, 3000, 5000} {
		r := NewResampler(inRate, outRate)
		out := r.Process(tone(freq, inRate, 48000))
		warm := r.TapsPerPhase()
		steady := out[warm : len(out)-warm]

		amp := amplitudeAt(steady, freq, outRate)
		if amp < 0.97 || amp > 1.03 {
			t.Errorf("%.0f Hz 通带幅度 %.4f，超出 1.0±3%%", freq, amp)
		}
	}
}

func TestDCGainIsUnityAcrossPhases(t *testing.T) {
	// 逐相归一化的目的就是这个：任何相位下直流增益都必须是 1，否则会听到一个
	// 与重采样比相关的低频调制音。44.1k→16k 有 160 个相位，最容易暴露问题。
	for _, rate := range []int{48000, 44100, 32000} {
		r := NewResampler(rate, 16000)
		in := make([]float32, rate) // 1 秒直流 0.5
		for i := range in {
			in[i] = 0.5
		}
		out := r.Process(in)
		warm := r.TapsPerPhase()
		steady := out[warm : len(out)-warm]

		lo, hi := float32(1), float32(-1)
		for _, v := range steady {
			lo, hi = min(lo, v), max(hi, v)
		}
		if math.Abs(float64(lo)-0.5) > 1e-4 || math.Abs(float64(hi)-0.5) > 1e-4 {
			t.Errorf("%d Hz：直流输出在 [%.6f, %.6f]，应恒为 0.5", rate, lo, hi)
		}
	}
}

func TestOutputRateIsCorrect(t *testing.T) {
	for _, tc := range []struct{ in, out int }{
		{48000, 16000}, {44100, 16000}, {32000, 16000}, {16000, 16000}, {8000, 16000},
	} {
		r := NewResampler(tc.in, tc.out)
		got := len(r.Process(make([]float32, tc.in))) // 送 1 秒
		// 允许 ±1 个样本的相位边界误差
		if got < tc.out-1 || got > tc.out+1 {
			t.Errorf("%d→%d：1 秒输入产出 %d 个样本，期望约 %d", tc.in, tc.out, got, tc.out)
		}
	}
}

// TestStreamingMatchesOneShot 守多相状态机的正确性：分块喂和一次喂必须逐样本
// 相同。采集回调给的块大小由驱动决定、并不固定，任何跨块的状态错误都会表现为
// 周期性的爆音，而那种问题在真实录音里极难定位。
func TestStreamingMatchesOneShot(t *testing.T) {
	in := tone(997, 48000, 48000) // 用非整除频率，避免碰巧对齐掩盖问题

	one := NewResampler(48000, 16000)
	want := append([]float32(nil), one.Process(in)...)

	for _, chunk := range []int{1, 7, 160, 480, 4801} {
		r := NewResampler(48000, 16000)
		var got []float32
		for i := 0; i < len(in); i += chunk {
			got = append(got, r.Process(in[i:min(i+chunk, len(in))])...)
		}
		if len(got) != len(want) {
			t.Fatalf("块长 %d：产出 %d 个样本，一次性产出 %d 个", chunk, len(got), len(want))
		}
		for i := range want {
			if math.Abs(float64(got[i]-want[i])) > 1e-6 {
				t.Fatalf("块长 %d：第 %d 个样本 %.7f != %.7f", chunk, i, got[i], want[i])
			}
		}
	}
}

func TestDownmixToMono(t *testing.T) {
	// 左声道有信号、右声道静音——有些会议软件就是这么放远端语音的。
	// 取平均能保住它（幅度减半），只取右声道会把人整个丢掉。
	stereo := []float32{1, 0, -1, 0, 0.5, 0}
	got := DownmixToMono(stereo, 2, nil)
	want := []float32{0.5, -0.5, 0.25}
	if len(got) != len(want) {
		t.Fatalf("长度 %d != %d", len(got), len(want))
	}
	for i := range want {
		if math.Abs(float64(got[i]-want[i])) > 1e-6 {
			t.Errorf("第 %d 个：%.4f != %.4f", i, got[i], want[i])
		}
	}
	if got := DownmixToMono(stereo, 1, nil); len(got) != len(stereo) {
		t.Error("单声道应原样返回")
	}

	// 复用缓冲：第二次传入上一次的结果，不能因为残留长度而串数据。
	buf := make([]float32, 0, 8)
	buf = DownmixToMono([]float32{1, 1, 1, 1, 1, 1, 1, 1}, 2, buf)
	if len(buf) != 4 {
		t.Fatalf("首次复用产出 %d 个，期望 4", len(buf))
	}
	buf = DownmixToMono([]float32{2, 2}, 2, buf)
	if len(buf) != 1 || math.Abs(float64(buf[0])-2) > 1e-6 {
		t.Errorf("复用后应为 [2]，得到 %v", buf)
	}
}

func TestFloatToInt16Clips(t *testing.T) {
	// 越界样本必须被限幅。直接乘 32767 会整数回绕，把一个响亮的瞬间变成爆音。
	got := FloatToInt16([]float32{0, 1, -1, 1.5, -1.5, 0.5}, nil)
	want := []int16{0, 32767, -32767, 32767, -32767, 16383}
	if len(got) != len(want) {
		t.Fatalf("长度 %d != %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 个：%d != %d", i, got[i], want[i])
		}
	}
}

func TestPeakLevel(t *testing.T) {
	if got := PeakLevel([]float32{0.1, -0.7, 0.3}); math.Abs(float64(got)-0.7) > 1e-6 {
		t.Errorf("峰值 %.4f != 0.7", got)
	}
	if got := PeakLevel(nil); got != 0 {
		t.Errorf("空输入峰值应为 0，得到 %.4f", got)
	}
}
