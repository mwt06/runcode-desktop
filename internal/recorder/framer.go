package recorder

// 定长分帧。
//
// 音频驱动交出来的缓冲大小由驱动自己定，且不保证恒定（WASAPI 上通常是 10 ms 的
// 整数倍，但会随系统负载抖动）。网关那边要的是与服务端 chunk 对齐的 600 ms 定长
// 帧。中间必须有这么一层，否则要么帧长忽大忽小，要么在每个回调里做一次不完整的
// 边界处理——后者正是跨块状态出错的高发地带。

// Framer 把任意长度的输入攒成定长帧。
//
// 不是并发安全的：一条音轨一个实例，由该轨的采集回调独占。
type Framer struct {
	size int
	buf  []int16
}

// NewFramer 建一个每帧 size 个样本的分帧器。
func NewFramer(size int) *Framer {
	return &Framer{size: size, buf: make([]int16, 0, size*2)}
}

// Push 追加样本，每攒够一帧就调一次 emit。
//
// emit 拿到的切片指向内部缓冲，回调返回后即失效——要留着必须自己拷贝。
func (f *Framer) Push(in []int16, emit func([]int16)) {
	f.buf = append(f.buf, in...)
	for len(f.buf) >= f.size {
		emit(f.buf[:f.size])
		f.buf = append(f.buf[:0], f.buf[f.size:]...)
	}
}

// Drain 把不足一帧的余量发出去，并清空。停止录音时调一次，否则最后不到 600 ms
// 的话会被丢掉——那往往正是「最后一句没了」的原因。
//
// 不补静音填满：补出来的是假音频，会让服务端的 VAD 多等一个空档。
func (f *Framer) Drain(emit func([]int16)) {
	if len(f.buf) > 0 {
		emit(f.buf)
		f.buf = f.buf[:0]
	}
}

// Pending 返回尚未凑满一帧的样本数。
func (f *Framer) Pending() int { return len(f.buf) }

// IsSilent 判断一帧是不是**数字静音**——即这一帧里根本没有信号被写入，而不是
// 「听起来很安静」。
//
// 这个区分是实测逼出来的（audioprobe 跑 10 秒，Realtek 板载声卡）：
//
//	回环轨  没有播放时严格全零：静音段里幅度 >300 的样本只有 6 个和 0 个
//	麦克风  空房间无人说话时仍有底噪，RMS 57、峰值 921（≈ 0.028 满量程）
//
// 所以这个函数对**回环轨是精确的**，对麦克风轨则永远返回 false——实测 17 帧
// 里 0 帧被判静音。这是正确行为，不是 bug：麦克风确实一直有声音进来。
//
// 由此定下的策略：**只有回环轨据此跳过上行**。麦克风轨一律上传，交给服务端
// 的 VAD 判断——那本来就是 VAD 的活。想在客户端也门控麦克风，就得按设备标定
// 底噪，而拿一台机器标出来的绝对阈值套到所有机器上，正是 FunASR 那边
// 「不要用合成数据标定声学参数」踩过的同一类坑。
//
// 阈值 32（≈ −60 dBFS）只用来区分「全零」与「非全零」，留一点余量给解码/
// 重采样的舍入误差，不是一个响度判据。
func IsSilent(pcm []int16) bool {
	const threshold = 32
	for _, v := range pcm {
		if v > threshold || v < -threshold {
			return false
		}
	}
	return true
}
