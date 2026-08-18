package recorder

// 有理重采样：把任意设备采样率归一到 16 kHz 单声道，喂给 FunASR 网关。
//
// 为什么要自己写：web 那版是 `new AudioContext({sampleRate:16000})`，重采样是
// 浏览器代劳的。Go 侧拿到的是设备原生格式（WASAPI 共享模式通常 48 kHz 立体声，
// 但用户在声音控制面板里改过就可能是 44.1 kHz），必须自己降。
//
// 为什么不能用「每 3 个取 1 个」：48 kHz 里 8 kHz 以上的成分在抽取后会**折叠**回
// 可听带——10 kHz 的哨声变成 6 kHz 的哨声，和真实语音混在一起再也分不开。识别
// 质量会莫名其妙地差，且极难归因到重采样这一步。所以抽取前必须先低通。
// resample_test.go 的 TestNoAliasing 守这条线：它同时跑一遍朴素抽取做对照，
// 证明这个坑是真的而不是假想。

import "math"

// 滤波器指标。阻带边界取输出奈奎斯特（越过它就会折叠），通带边界留 12.5% 余量
// 当过渡带——16 kHz 输出就是「7 kHz 以下平坦，8 kHz 以上压死」。语音在 7 kHz
// 以上能量已经很少，这个取舍是划算的。
const (
	passbandFrac = 0.875
	// 单个多相分支的抽头数上限。防止病态采样率把初始化时间和内存拉爆——正常
	// 的 44.1k/48k 算出来是 240~270，离这个上限很远。
	maxTapsPerPhase = 512
	// 窗函数法的经验系数：Blackman 窗下过渡带宽约为 5.5/N（N 为抽头数）。
	blackmanTransition = 5.5
)

// Resampler 是流式有理重采样器（L 倍插值 + M 倍抽取的多相实现）。
// 只算真正需要的输出点，不会先展开成 L 倍长的中间信号。
//
// 不是并发安全的：一条音轨一个实例，由该轨的采集回调独占。
type Resampler struct {
	l, m int       // 插值/抽取比，已按最大公约数化简
	k    int       // 每个多相分支的抽头数 = len(taps)/l
	taps []float32 // 原型低通滤波器

	// 多相状态。phase/baseAbs 用增量推进而不是每次算 n*m，既避免长录音下的溢出，
	// 也省掉每个输出点的一次乘法和除法。
	phase   int
	baseAbs int64
	bufAbs  int64     // buf[0] 对应的绝对输入下标
	buf     []float32 // 输入历史 + 待处理样本
	out     []float32 // 复用的输出缓冲，避免每帧分配
}

// NewResampler 建一个 inRate → outRate 的重采样器。
// inRate == outRate 时退化成直通（不建滤波器，零开销）。
func NewResampler(inRate, outRate int) *Resampler {
	g := gcd(inRate, outRate)
	r := &Resampler{l: outRate / g, m: inRate / g}
	if r.l == 1 && r.m == 1 {
		return r // 直通
	}

	// 阻带边界由输入/输出里较低的那个奈奎斯特决定：降采样时是输出侧（防折叠），
	// 升采样时是输入侧（防镜像）。
	stop := float64(min(inRate, outRate)) / 2
	pass := stop * passbandFrac
	upRate := float64(r.l) * float64(inRate) // 插值后的等效速率

	n := int(math.Ceil(blackmanTransition / ((stop - pass) / upRate)))
	n = ((n + r.l - 1) / r.l) * r.l // 向上取到 l 的整数倍，好切多相
	if n/r.l > maxTapsPerPhase {
		n = maxTapsPerPhase * r.l
	}
	if n < 8*r.l {
		n = 8 * r.l
	}

	fc := (pass + stop) / 2 / upRate // 截止取通带与阻带的中点
	r.taps = designLowpass(n, fc, r.l)
	r.k = n / r.l

	// 预置 k-1 个零作为滤波器预热历史，这样第一个输出点就有完整抽头可用，
	// 不必对流的开头做特判。
	r.bufAbs = -int64(r.k - 1)
	r.buf = make([]float32, r.k-1)
	return r
}

// Ratio 返回化简后的 L/M，给测试和日志用。
func (r *Resampler) Ratio() (l, m int) { return r.l, r.m }

// TapsPerPhase 返回每个多相分支的抽头数（直通时为 0）。
func (r *Resampler) TapsPerPhase() int { return r.k }

// Process 送入一段输入样本，返回这一段能产出的输出样本。
//
// 返回的切片由 Resampler 复用，调用方必须在下次 Process 之前用完或自己拷贝。
// 这是为了避免每 600 ms 一次的堆分配——录一小时就是 6000 次。
func (r *Resampler) Process(in []float32) []float32 {
	if r.l == 1 && r.m == 1 {
		return in
	}

	r.buf = append(r.buf, in...)
	r.out = r.out[:0]

	lastAbs := r.bufAbs + int64(len(r.buf)) - 1
	for r.baseAbs <= lastAbs {
		// y[n] = Σ_k h[phase + k·L] · x[base − k]
		var acc float32
		off := int(r.baseAbs - r.bufAbs)
		for k := 0; k < r.k; k++ {
			acc += r.taps[r.phase+k*r.l] * r.buf[off-k]
		}
		r.out = append(r.out, acc)

		// 推进到下一个输出点：等价于 t += m，再把 t 拆回 phase 与 base 的进位。
		t := r.phase + r.m
		r.phase = t % r.l
		r.baseAbs += int64(t / r.l)
	}

	// 丢掉不再被任何未来输出点引用的历史，保留 k-1 个。
	if drop := int(r.baseAbs - int64(r.k-1) - r.bufAbs); drop > 0 {
		r.buf = append(r.buf[:0], r.buf[drop:]...)
		r.bufAbs += int64(drop)
	}
	return r.out
}

// designLowpass 造一个窗函数法低通原型：sinc × Blackman。
//
// 做**逐相归一化**而不是整体归一化：下标同余 l 的抽头属于同一个多相分支，各自
// 求和归一，保证任何相位下的直流增益都精确为 1。不然不同相位的增益有微小差异，
// 在信号上表现为一个与重采样比相关的低频调制音。顺带把插值补零摊薄的 l 倍能量
// 也一并补回来了，不用再单独乘 l。
func designLowpass(n int, fc float64, l int) []float32 {
	h := make([]float64, n)
	center := float64(n-1) / 2
	for i := 0; i < n; i++ {
		x := float64(i) - center
		var s float64
		if x == 0 {
			s = 2 * fc
		} else {
			s = math.Sin(2*math.Pi*fc*x) / (math.Pi * x)
		}
		// Blackman 窗：阻带衰减约 74 dB，对语音绰绰有余
		w := 0.42 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1)) +
			0.08*math.Cos(4*math.Pi*float64(i)/float64(n-1))
		h[i] = s * w
	}

	for p := 0; p < l; p++ {
		var sum float64
		for k := p; k < n; k += l {
			sum += h[k]
		}
		if sum == 0 {
			continue
		}
		for k := p; k < n; k += l {
			h[k] /= sum
		}
	}

	out := make([]float32, n)
	for i, v := range h {
		out[i] = float32(v)
	}
	return out
}

// DownmixToMono 把交错的多声道样本合成单声道。
//
// 系统回环几乎总是立体声，而 ASR 要单声道。取平均而不是只取左声道——有些会议
// 软件把远端语音只放在一个声道上，只取一个声道会把人整个丢掉。
//
// 结果写进 out（复用/按需扩容）。out 由调用方跨回调持有：这个函数跑在音频回调
// 里，约 100 Hz × 2 轨，在这里分配是实时路径抖动的经典来源。
// channels <= 1 时直接返回入参，不拷贝。
func DownmixToMono(interleaved []float32, channels int, out []float32) []float32 {
	if channels <= 1 {
		return interleaved
	}
	n := len(interleaved) / channels
	out = out[:0]
	inv := 1 / float32(channels)
	for i := 0; i < n; i++ {
		var acc float32
		base := i * channels
		for c := 0; c < channels; c++ {
			acc += interleaved[base+c]
		}
		out = append(out, acc*inv)
	}
	return out
}

// FloatToInt16 转成网关要的 PCM s16le，并做硬限幅。
//
// 限幅是必须的：多声道合成和滤波器的过冲都可能让样本越过 ±1，直接乘 32767 会
// 整数回绕，一个响亮的瞬间会变成刺耳的爆音。
func FloatToInt16(in []float32, out []int16) []int16 {
	out = out[:0]
	for _, v := range in {
		switch {
		case v > 1:
			v = 1
		case v < -1:
			v = -1
		}
		out = append(out, int16(v*32767))
	}
	return out
}

// PeakLevel 返回这一段的峰值幅度（0..1），给界面的电平表用。
func PeakLevel(in []float32) float32 {
	var peak float32
	for _, v := range in {
		if v < 0 {
			v = -v
		}
		if v > peak {
			peak = v
		}
	}
	return peak
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
