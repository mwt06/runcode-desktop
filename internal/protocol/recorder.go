package protocol

// 录音纪要的命令面与事件。
//
// 这一层只做 DTO：internal/recorder 的类型（SessionMeta、TrackMeta、Event）
// 是采集与上行的内部形状，不该直接上线到前端——它们会随协议演进改字段，而
// 界面契约要稳。转换写在 desktop/recorder.go 里。

// RecorderDevice 是「麦克风 ∨」下拉里的一项。
type RecorderDevice struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
}

// RecorderDeviceList 是两路音源各自可选的设备。
//
// Supported=false 表示这台机器上根本开不了录音（非 Windows、或 WASAPI 初始化
// 失败）。分开报而不是直接返回 error：界面要把「录音纪要」入口置灰并给出理由，
// 而不是等用户点下去才弹一个错。
type RecorderDeviceList struct {
	Mic       []RecorderDevice `json:"mic"`
	Sys       []RecorderDevice `json:"sys"`
	Supported bool             `json:"supported"`
	Reason    string           `json:"reason,omitempty"`
}

// StartRecordingRequest 开一场录音。空字段一律回落到 RecorderSettings 里存的值。
type StartRecordingRequest struct {
	// Title 为空时取「新录音 N」，N 是目录里已有的最大序号加一。
	Title string `json:"title"`
	// Lang 对应界面上的「自动识别 ∨」，空 = 自动。
	Lang string `json:"lang"`
	// MicDeviceID / SysDeviceID 为空表示用系统默认设备。
	MicDeviceID string `json:"micDeviceId"`
	SysDeviceID string `json:"sysDeviceId"`
}

// RecorderTrack 是一条音轨录完后的记录。
type RecorderTrack struct {
	// Source 是 "mic"（我）或 "sys"（对方，系统回环）。
	Source     string `json:"source"`
	WAV        string `json:"wav"`
	SampleRate int    `json:"sampleRate"`
	Channels   int    `json:"channels"`
	DurationMS int64  `json:"durationMs"`
	// GapMS > 0 意味着服务端那份转写有缺口（断线期间的音频按设计直接丢弃，
	// 不重放），会后要拿本地 WAV 补一次。
	GapMS      int64 `json:"gapMs"`
	SentFrames int64 `json:"sentFrames"`
	Dropped    int64 `json:"droppedFrames"`
	Reconnects int64 `json:"reconnects"`
}

// RecordingInfo 是一场录音在界面上的样子。ID 为空表示「当前没有录音」。
type RecordingInfo struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Room 是网关侧的房间号，两条轨靠它归到同一条时间轴上。
	Room string `json:"room"`
	// State 是 idle / recording / paused / stopping / stopped。
	State string `json:"state"`
	Lang  string `json:"lang,omitempty"`
	// StartedAt / EndedAt 是 RFC3339；EndedAt 为空表示还没结束。
	StartedAt string `json:"startedAt"`
	EndedAt   string `json:"endedAt,omitempty"`
	// AudioMS 是真正录到的时长，不含暂停——它和墙上时间会对不上，界面上的
	// 计时器要显示的是它。
	AudioMS int64 `json:"audioMs"`
	// Interrupted 表示上次不是正常结束的（进程被强杀 / 断电），由启动时的恢复
	// 扫描打上。界面据此提示「有一场未完成的录音」。
	Interrupted bool `json:"interrupted,omitempty"`
	// NeedsBackfill 表示服务端那份转写不完整，要拿本地音频补跑。
	NeedsBackfill bool            `json:"needsBackfill,omitempty"`
	Tracks        []RecorderTrack `json:"tracks,omitempty"`
	// Uplink 是上行链路状态：connecting / connected / offline / stopped。
	// offline 时界面显示「离线录制中」——本地照录，转写会有缺口。
	Uplink string `json:"uplink,omitempty"`
	// Dir 是这场录音的落盘目录，「在文件夹中显示」用。
	Dir string `json:"dir,omitempty"`
}

// RecorderSettings 是录音纪要的客户端设置，落在配置目录里，与会话无关。
type RecorderSettings struct {
	// GatewayURL 是 FunASR 网关地址（ws:// 或 wss://）。空 = 未配置，
	// 此时不允许开始录音。
	GatewayURL string `json:"gatewayUrl"`
	// SpeakerName 是麦克风轨的说话人显示名，空则取通行证里的姓名。
	SpeakerName string `json:"speakerName"`
	// Lang 是默认识别语言，空 = 自动。
	Lang string `json:"lang"`
	// MicDeviceID / SysDeviceID 记住上次选的设备，空 = 系统默认。
	MicDeviceID string `json:"micDeviceId"`
	SysDeviceID string `json:"sysDeviceId"`
	// KeepAudio 决定转写完成后是否保留本地 WAV。默认保留：它是补转写唯一的
	// 依据，删早了就没有第二次机会。
	KeepAudio bool `json:"keepAudio"`
	// SummaryModel 是实时总结用的模型名（客户端设置，走用户自己的额度）。
	// 空 = 关闭实时总结。
	SummaryModel string `json:"summaryModel"`
	// Root 是录音落盘根目录。空 = 平台默认（见 desktop.recorderRoot）。
	Root string `json:"root,omitempty"`
}

// RecorderTranscript 是网关下行的一条转写事件。
//
// 字段与 recorder.Event 一一对应，含义见那边——这里刻意不复述，两处各写一遍
// 只会各自漂移。
type RecorderTranscript struct {
	Type  string `json:"type"`
	Track string `json:"track,omitempty"`
	Seg   int    `json:"seg"`
	Rev   int    `json:"rev"`
	Text  string `json:"text,omitempty"`
	BT    int64  `json:"bt,omitempty"`
	ET    int64  `json:"et,omitempty"`

	Spk    string  `json:"spk,omitempty"`
	Name   string  `json:"name,omitempty"`
	UserID string  `json:"userId,omitempty"`
	Conf   float64 `json:"conf,omitempty"`

	Segs []int `json:"segs,omitempty"`

	Silence int `json:"silence,omitempty"`
	Need    int `json:"need,omitempty"`

	Stage    string `json:"stage,omitempty"`
	QueuePos int    `json:"queuePos,omitempty"`
}

// RecorderState 是录音状态的变化。
type RecorderState struct {
	ID    string `json:"id"`
	State string `json:"state"`
	// AudioMS 让界面的计时器不必自己数——暂停期间它不涨，本地重算容易和
	// 落盘的时长对不上。
	AudioMS int64  `json:"audioMs"`
	Uplink  string `json:"uplink,omitempty"`
	// Error 非空表示这场录音是被错误终止的（磁盘写不动、设备掉了）。
	Error string `json:"error,omitempty"`
}

// RecorderLevel 是两条轨的实时电平（0..1），约 20 Hz。
//
// 两条轨合成一条事件发：它们各自约 20 Hz，分开发就是 40 次/秒的事件，而界面
// 上是同一次重绘要用的两个数。
type RecorderLevel struct {
	Mic float32 `json:"mic"`
	Sys float32 `json:"sys"`
}

// 录音纪要的事件名。
const (
	// EventRecorderTranscript carries a RecorderTranscript：网关下行的每一条
	// partial / final / drop / respeaker / live_status 都原样透传给界面。
	EventRecorderTranscript = "recorder:transcript"
	// EventRecorderState carries a RecorderState：开始、暂停、恢复、结束，
	// 以及上行链路状态变化。
	EventRecorderState = "recorder:state"
	// EventRecorderLevel carries a RecorderLevel：电平条的数据。
	EventRecorderLevel = "recorder:level"
)
