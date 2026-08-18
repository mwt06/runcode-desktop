package recorder

// 双轨 WAV 落盘。
//
// 这是整个录音功能的可靠性地基：**先落盘，再上行**，顺序不能反。网络断了、
// 服务端实例挂了、GPU 排队排到天荒地老，本地这份录音都必须是完整的。
//
// 落盘还要能扛住进程被强杀。做法是每次 Flush 都把 RIFF 头里的两个长度字段
// 回写一遍——这样任何时刻抢到的文件都是一个合法的 WAV，用播放器直接能开。
// 最后一次 Flush 之后写入的那点数据（≤ SyncEvery 对应的时长）头里没算进去，
// RepairWAV 按实际文件大小补回来。

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"time"
)

const (
	wavHeaderSize  = 44
	bytesPerSample = 2 // s16le

	// defaultSyncEvery 是两次 fsync 之间累积的音频时长。5 秒是个取舍：再短，
	// 一小时录音要 fsync 上千次，机械盘上会有可感的卡顿；再长，崩溃时靠
	// RepairWAV 找回的那段就更长，而那段数据的完整性没有 fsync 背书。
	defaultSyncEvery = 5 * time.Second

	// maxDataBytes 是 WAV 能描述的最大音频数据量：RIFF 与 data 两个长度字段
	// 都是 uint32，再加上 36 字节的头部开销。
	//
	// 16 kHz 单声道下约合 37 小时。听起来远得很，但「开完会忘了停」是真会
	// 发生的事，而越界的后果是**静默写出一个坏头**——文件看着在长，播放器
	// 只认前面一小截。所以这里宁可显式报错也不让它悄悄发生。
	maxDataBytes = math.MaxUint32 - 36

	// 录音是用户的私有数据，文件权限跟仓库既有约定一致（目录 0755、文件 0600）。
	wavFileMode = 0o600
)

// WAVSink 是 s16le 的流式 WAV 写入器。
//
// 不是并发安全的：一条音轨一个实例，由该轨的写盘 goroutine 独占。
type WAVSink struct {
	f *os.File
	// 采样率与声道数直接按 WAV 头里的宽度存，构造时校验一次。这样写头时不再
	// 有任何可能越界的窄化转换，正确性由类型本身保证，而不是靠注释。
	sampleRate uint32
	channels   uint16
	blockAlign uint16

	// SyncEvery 是自动 fsync 的间隔（按音频时长算，不是墙上时钟——这样测试
	// 是确定性的，而且真正要紧的本来就是「丢了多少秒录音」）。
	SyncEvery time.Duration

	written  int64 // 已写入的样本帧数
	syncedAt int64 // 上次 Flush 时的 written
	closed   bool
	scratch  []byte
}

// narrow 在调用方已经校验过范围之后做窄化转换。
//
// 单独抽出来是为了把 gosec 的豁免集中在一处，而不是散落在构造函数里：G115 不做
// 流敏感分析，看不见 NewWAVSink 开头那两行范围检查。用它之前请确认调用点确实
// 已经校验过——这个函数本身不校验任何东西。
func narrow[T uint16 | uint32](v int) T {
	return T(v) //nolint:gosec // 调用方已校验范围，见 NewWAVSink 的入口检查
}

// NewWAVSink 建文件并占位写下 44 字节头。文件已存在会被截断。
func NewWAVSink(path string, sampleRate, channels int) (*WAVSink, error) {
	// 上界不是形式主义：这两个值最终要塞进 WAV 头的 uint32/uint16 字段，
	// 在入口挡住比在写头时截断要好查得多。
	if sampleRate <= 0 || sampleRate > math.MaxInt32 {
		return nil, fmt.Errorf("非法采样率 %d", sampleRate)
	}
	if channels <= 0 || channels > math.MaxUint8 {
		return nil, fmt.Errorf("非法声道数 %d", channels)
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, wavFileMode)
	if err != nil {
		return nil, fmt.Errorf("建录音文件: %w", err)
	}
	w := &WAVSink{
		f:          f,
		sampleRate: narrow[uint32](sampleRate),
		channels:   narrow[uint16](channels),
		blockAlign: narrow[uint16](channels * bytesPerSample),
		SyncEvery:  defaultSyncEvery,
	}
	if _, err := f.Write(w.header(0)); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("写 WAV 头: %w", err)
	}
	return w, nil
}

// Write 追加样本。攒够 SyncEvery 对应的时长会自动 Flush 一次。
func (w *WAVSink) Write(samples []int16) error {
	if w.closed {
		return errors.New("WAVSink 已关闭")
	}
	if len(samples) == 0 {
		return nil
	}

	need := len(samples) * bytesPerSample
	if w.dataBytes()+int64(need) > maxDataBytes {
		// 到这一步已录的部分仍然完好——头是对的，文件能播。停在这里比写出
		// 一个长度字段回绕过的坏文件强。
		return fmt.Errorf("录音已达 WAV 格式上限（约 %.0f 小时），请先结束本次录音",
			float64(maxDataBytes)/float64(w.sampleRate)/float64(w.blockAlign)/3600)
	}

	if cap(w.scratch) < need {
		w.scratch = make([]byte, need)
	}
	b := w.scratch[:need]
	for i, s := range samples {
		//nolint:gosec // int16→uint16 是等宽位重解释，无信息损失；encoding/binary 只收 uint16
		binary.LittleEndian.PutUint16(b[i*2:], uint16(s))
	}
	if _, err := w.f.Write(b); err != nil {
		return fmt.Errorf("写录音数据: %w", err)
	}
	w.written += int64(len(samples)) / int64(w.channels)

	if w.pendingDuration() >= w.SyncEvery {
		return w.Flush()
	}
	return nil
}

// Flush 回写 RIFF 头的长度字段并 fsync。调用后文件在磁盘上是一个完整合法的 WAV。
func (w *WAVSink) Flush() error {
	if w.closed {
		return errors.New("WAVSink 已关闭")
	}
	h := w.header(w.dataBytes())
	// 只回写两个长度字段，不重写整个头——省一次 seek，也避免把 fmt 块写坏。
	if _, err := w.f.WriteAt(h[4:8], 4); err != nil {
		return fmt.Errorf("回写 RIFF 长度: %w", err)
	}
	if _, err := w.f.WriteAt(h[40:44], 40); err != nil {
		return fmt.Errorf("回写 data 长度: %w", err)
	}
	if err := w.f.Sync(); err != nil {
		return fmt.Errorf("fsync 录音文件: %w", err)
	}
	w.syncedAt = w.written
	return nil
}

// Close 做最后一次 Flush 并关闭文件。重复调用是安全的。
func (w *WAVSink) Close() error {
	if w.closed {
		return nil
	}
	err := w.Flush()
	w.closed = true
	if cerr := w.f.Close(); err == nil {
		err = cerr
	}
	return err
}

// Samples 返回已写入的样本帧数。
func (w *WAVSink) Samples() int64 { return w.written }

// Duration 返回已录制的时长。
func (w *WAVSink) Duration() time.Duration {
	return time.Duration(w.written) * time.Second / time.Duration(w.sampleRate)
}

// dataBytes 是当前音频数据的字节数。Write 已挡住越界，所以它必然 ≤ maxDataBytes。
func (w *WAVSink) dataBytes() int64 { return w.written * int64(w.blockAlign) }

// pendingDuration 是自上次 Flush 以来、尚未 fsync 的音频时长。
func (w *WAVSink) pendingDuration() time.Duration {
	return time.Duration(w.written-w.syncedAt) * time.Second / time.Duration(w.sampleRate)
}

// header 造 44 字节的规范 WAV 头。dataBytes 由调用方保证 ≤ maxDataBytes。
func (w *WAVSink) header(dataBytes int64) []byte {
	//nolint:gosec // min 已把值夹到 maxDataBytes(= MaxUint32-36)，必然在 uint32 范围内
	size := uint32(min(dataBytes, maxDataBytes))

	h := make([]byte, wavHeaderSize)
	copy(h[0:], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], 36+size)
	copy(h[8:], "WAVE")
	copy(h[12:], "fmt ")
	binary.LittleEndian.PutUint32(h[16:], 16) // fmt 块长度
	binary.LittleEndian.PutUint16(h[20:], 1)  // PCM
	binary.LittleEndian.PutUint16(h[22:], w.channels)
	binary.LittleEndian.PutUint32(h[24:], w.sampleRate)
	binary.LittleEndian.PutUint32(h[28:], w.sampleRate*uint32(w.blockAlign)) // byteRate
	binary.LittleEndian.PutUint16(h[32:], w.blockAlign)
	binary.LittleEndian.PutUint16(h[34:], 16) // 位深
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], size)
	return h
}

// RepairWAV 按文件实际大小修正头里的长度字段，返回修正后的样本帧数。
//
// 用在进程被强杀之后：最后一次 Flush 之后写入的数据在文件里，但头没算进去，
// 播放器和解码器都会按头里的短长度截断。这是方案 §08「录音状态可从磁盘恢复」
// 那条的一半——另一半是会话元数据。
//
// 幂等：文件本来就完好时不做任何写入。
func RepairWAV(path string) (frames int64, err error) {
	f, err := os.OpenFile(path, os.O_RDWR, wavFileMode)
	if err != nil {
		return 0, fmt.Errorf("打开录音文件: %w", err)
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()

	st, err := f.Stat()
	if err != nil {
		return 0, fmt.Errorf("读文件信息: %w", err)
	}
	if st.Size() < wavHeaderSize {
		return 0, fmt.Errorf("文件只有 %d 字节，连 WAV 头都不完整", st.Size())
	}

	var h [wavHeaderSize]byte
	if _, err := f.ReadAt(h[:], 0); err != nil {
		return 0, fmt.Errorf("读 WAV 头: %w", err)
	}
	if string(h[0:4]) != "RIFF" || string(h[8:12]) != "WAVE" {
		return 0, errors.New("不是 WAV 文件")
	}
	blockAlign := int64(binary.LittleEndian.Uint16(h[32:]))
	if blockAlign <= 0 {
		return 0, errors.New("WAV 头里的 blockAlign 非法")
	}

	actual := st.Size() - wavHeaderSize
	actual -= actual % blockAlign // 截掉半个样本帧
	if actual > maxDataBytes {
		return 0, fmt.Errorf("文件 %d 字节，超出 WAV 头能描述的上限", st.Size())
	}
	frames = actual / blockAlign

	if int64(binary.LittleEndian.Uint32(h[40:])) == actual {
		return frames, nil // 已经是对的，不写
	}

	//nolint:gosec // 上面刚判过 actual > maxDataBytes 就返回，到这里必然在范围内
	size := uint32(actual)
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], 36+size)
	if _, err := f.WriteAt(buf[:], 4); err != nil {
		return 0, fmt.Errorf("回写 RIFF 长度: %w", err)
	}
	binary.LittleEndian.PutUint32(buf[:], size)
	if _, err := f.WriteAt(buf[:], 40); err != nil {
		return 0, fmt.Errorf("回写 data 长度: %w", err)
	}
	return frames, f.Sync()
}
