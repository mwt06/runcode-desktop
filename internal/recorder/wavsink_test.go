package recorder

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// wavInfo 解析 WAV 头，返回声明的数据长度与格式参数。
type wavInfo struct {
	channels   int
	sampleRate int
	bits       int
	dataSize   int64
	fileSize   int64
}

func readWAV(t *testing.T, path string) wavInfo {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读文件: %v", err)
	}
	if len(b) < wavHeaderSize {
		t.Fatalf("文件只有 %d 字节", len(b))
	}
	if string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" || string(b[36:40]) != "data" {
		t.Fatalf("不是规范的 WAV 头")
	}
	riff := int64(binary.LittleEndian.Uint32(b[4:]))
	data := int64(binary.LittleEndian.Uint32(b[40:]))
	if riff != 36+data {
		t.Errorf("RIFF 长度 %d 与 data 长度 %d 不自洽", riff, data)
	}
	return wavInfo{
		channels:   int(binary.LittleEndian.Uint16(b[22:])),
		sampleRate: int(binary.LittleEndian.Uint32(b[24:])),
		bits:       int(binary.LittleEndian.Uint16(b[34:])),
		dataSize:   data,
		fileSize:   int64(len(b)),
	}
}

func ramp(n int) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(i % 1000)
	}
	return out
}

func TestWAVSinkWritesValidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.wav")
	w, err := NewWAVSink(path, 16000, 1)
	if err != nil {
		t.Fatalf("建 sink: %v", err)
	}
	if err := w.Write(ramp(16000)); err != nil { // 1 秒
		t.Fatalf("写: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("关闭: %v", err)
	}

	info := readWAV(t, path)
	if info.channels != 1 || info.sampleRate != 16000 || info.bits != 16 {
		t.Errorf("格式不对：%d 声道 / %d Hz / %d bit", info.channels, info.sampleRate, info.bits)
	}
	if want := int64(16000 * 2); info.dataSize != want {
		t.Errorf("data 长度 %d，期望 %d", info.dataSize, want)
	}
	if info.fileSize != wavHeaderSize+info.dataSize {
		t.Errorf("文件大小 %d 与头声明的 %d 对不上", info.fileSize, wavHeaderSize+info.dataSize)
	}
	if got := w.Duration(); got != time.Second {
		t.Errorf("时长 %v，期望 1s", got)
	}
}

// TestWAVSinkSurvivesCrash 是这个类型存在的核心理由之一：进程被强杀时，磁盘上
// 那份文件必须已经是个能直接播放的合法 WAV，而不是一堆需要专用工具才能救的字节。
func TestWAVSinkSurvivesCrash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crash.wav")
	w, err := NewWAVSink(path, 16000, 1)
	if err != nil {
		t.Fatalf("建 sink: %v", err)
	}
	w.SyncEvery = time.Second

	// 写 3 秒，会触发 3 次自动 Flush。
	for i := 0; i < 3; i++ {
		if err := w.Write(ramp(16000)); err != nil {
			t.Fatalf("写: %v", err)
		}
	}
	// 再写 0.5 秒——不够触发 Flush，模拟「崩在两次 fsync 之间」。
	if err := w.Write(ramp(8000)); err != nil {
		t.Fatalf("写: %v", err)
	}

	// 此刻强杀：直接丢掉句柄，不走 Close（也就不会 patch 头）。这正是进程被
	// 强杀后文件的样子——数据在盘上，头还停在上次 fsync 的时刻。
	// Windows 上必须真的释放句柄，否则 t.TempDir 的清理删不掉这个文件。
	if err := w.f.Close(); err != nil {
		t.Fatalf("模拟崩溃时释放句柄: %v", err)
	}
	w.closed = true

	info := readWAV(t, path)
	if want := int64(3 * 16000 * 2); info.dataSize != want {
		t.Errorf("崩溃现场的头应记录已 fsync 的 3 秒（%d 字节），实际 %d", want, info.dataSize)
	}
	if info.fileSize != wavHeaderSize+int64(3.5*16000*2) {
		t.Errorf("文件里应该有 3.5 秒的数据，实际 %d 字节", info.fileSize)
	}

	// RepairWAV 把最后那 0.5 秒找回来。
	frames, err := RepairWAV(path)
	if err != nil {
		t.Fatalf("修复: %v", err)
	}
	if want := int64(3.5 * 16000); frames != want {
		t.Errorf("修复后 %d 帧，期望 %d", frames, want)
	}
	fixed := readWAV(t, path)
	if fixed.dataSize != fixed.fileSize-wavHeaderSize {
		t.Errorf("修复后头仍与文件大小不符：%d vs %d", fixed.dataSize, fixed.fileSize-wavHeaderSize)
	}
}

func TestRepairWAVIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ok.wav")
	w, _ := NewWAVSink(path, 16000, 1)
	_ = w.Write(ramp(1600))
	_ = w.Close()

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读: %v", err)
	}
	frames, err := RepairWAV(path)
	if err != nil {
		t.Fatalf("修复完好文件不该出错: %v", err)
	}
	if frames != 1600 {
		t.Errorf("帧数 %d != 1600", frames)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("文件本来就完好，RepairWAV 不应改动它")
	}
}

func TestRepairWAVRejectsGarbage(t *testing.T) {
	dir := t.TempDir()

	short := filepath.Join(dir, "short.bin")
	if err := os.WriteFile(short, []byte("RIFF"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RepairWAV(short); err == nil {
		t.Error("只有 4 字节的文件应当报错")
	}

	notWav := filepath.Join(dir, "not.bin")
	if err := os.WriteFile(notWav, make([]byte, 128), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RepairWAV(notWav); err == nil {
		t.Error("非 WAV 文件应当报错")
	}
}

func TestWAVSinkCloseIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.wav")
	w, _ := NewWAVSink(path, 16000, 1)
	_ = w.Write(ramp(160))
	if err := w.Close(); err != nil {
		t.Fatalf("首次关闭: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("重复关闭应当无害，得到 %v", err)
	}
	if err := w.Write(ramp(160)); err == nil {
		t.Error("关闭后写入应当报错")
	}
}

func TestWAVSinkStereoAccounting(t *testing.T) {
	// 立体声下 Samples/Duration 算的是**帧**不是样本个数。回环轨在降混之前
	// 是立体声，这个换算错了会让时长翻倍，进而让房间时间轴整体错位。
	path := filepath.Join(t.TempDir(), "s.wav")
	w, err := NewWAVSink(path, 48000, 2)
	if err != nil {
		t.Fatalf("建 sink: %v", err)
	}
	if err := w.Write(make([]int16, 48000*2)); err != nil { // 1 秒立体声
		t.Fatalf("写: %v", err)
	}
	if got := w.Samples(); got != 48000 {
		t.Errorf("帧数 %d，期望 48000", got)
	}
	if got := w.Duration(); got != time.Second {
		t.Errorf("时长 %v，期望 1s", got)
	}
	_ = w.Close()
}
