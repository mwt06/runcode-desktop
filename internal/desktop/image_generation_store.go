package desktop

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	appwire "github.com/wt68/runcode/internal/protocol"
)

// 文生图的工作区内存储：会话开关 sidecar 与每次生成的提交账本。全部经 os.OpenRoot
// 限定在会话工作区内，写入走临时文件 + fsync + 原子替换；读到损坏内容一律按存储
// 失败处理，不当成"没有记录"。

var errImageGenerationStorage = errors.New("图片生成记录无法读取或保存，请检查工作区存储后重试")

const (
	imageGenerationStoreVersion = 1
	imageGenerationSidecarLimit = 64 * 1024
)

// validImageGenerationID 接受此处用作文件名的不透明 id（会话／generation／回合）：字母数字与 _-。
func validImageGenerationID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

func newImageGenerationID(prefix string) string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

// readRootedFile 读取工作区下的 rel。exists=false 才是文件确实不存在；
// 祖先不是目录算存储损坏而非缺失（Windows 此时也会报 ENOENT）。
func readRootedFile(workspace, rel string, limit int64) (data []byte, exists bool, err error) {
	if workspace == "" {
		return nil, false, errImageGenerationStorage
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return nil, false, errImageGenerationStorage
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(rel)
	if errors.Is(err, os.ErrNotExist) {
		for parent := filepath.Dir(rel); ; parent = filepath.Dir(parent) {
			info, statErr := root.Stat(parent)
			if statErr == nil {
				if !info.IsDir() {
					return nil, false, errImageGenerationStorage
				}
				return nil, false, nil
			}
			if !errors.Is(statErr, os.ErrNotExist) || filepath.Dir(parent) == parent {
				return nil, false, errImageGenerationStorage
			}
		}
	}
	if err != nil {
		return nil, false, errImageGenerationStorage
	}
	defer func() { _ = f.Close() }()
	data, err = io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, true, errImageGenerationStorage
	}
	return data, true, nil
}

// writeRootedFileAtomic 持久替换 rel：读者只见旧内容或新内容，不会读到半截。
func writeRootedFileAtomic(workspace, rel string, data []byte) error {
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return errImageGenerationStorage
	}
	defer func() { _ = root.Close() }()
	if err := root.MkdirAll(filepath.Dir(rel), 0o700); err != nil {
		return errImageGenerationStorage
	}
	tmp := rel + "." + rand.Text() + ".tmp"
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errImageGenerationStorage
	}
	defer func() { _ = root.Remove(tmp) }()
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if errors.Join(writeErr, syncErr, closeErr) != nil {
		return errImageGenerationStorage
	}
	if err := root.Rename(tmp, rel); err != nil {
		return errImageGenerationStorage
	}
	return nil
}

// deleteImageGenerationSessionRecords 只清理随会话派生的三类记录：
// 会话开关、请求账本与回合标记。生成的图片属于用户产物，刻意不在这里删除。
//
// 所有路径都经 os.Root 限定在工作区内，且先校验不透明会话 id，避免路径穿越。
func deleteImageGenerationSessionRecords(workspace, id string) error {
	if !validImageGenerationID(id) {
		return errImageGenerationStorage
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return errImageGenerationStorage
	}
	defer func() { _ = root.Close() }()

	base := filepath.Join(".runcode", "image-generation")
	paths := []string{
		filepath.Join(base, "sessions", id+".json"),
		filepath.Join(base, "requests", id),
		filepath.Join(base, "turns", id),
	}
	for _, path := range paths {
		if err := root.RemoveAll(path); err != nil {
			return errImageGenerationStorage
		}
	}
	return nil
}

// createRootedFileExclusive 排他创建 rel 并 sync；已存在则回报 os.ErrExist，绝不覆盖。
func createRootedFileExclusive(workspace, rel string, data []byte) error {
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return errImageGenerationStorage
	}
	defer func() { _ = root.Close() }()
	if err := root.MkdirAll(filepath.Dir(rel), 0o700); err != nil {
		return errImageGenerationStorage
	}
	f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return os.ErrExist
	}
	if err != nil {
		return errImageGenerationStorage
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if errors.Join(writeErr, syncErr, closeErr) != nil {
		_ = root.Remove(rel)
		return errImageGenerationStorage
	}
	return nil
}

// —— 提交账本 ——
//
// 每次生成一个记录：.runcode/image-generation/requests/<session>/<generation>.json。
// POST 之前先写成 submitted 并 fsync；写不进去就不提交。另有每回合一个排他创建的
// 标记 .runcode/image-generation/turns/<session>/<turn>.json，它是跨工具实例、跨进程的
// 「本回合已经提交过」的权威依据，任何结果下都不删除。这是客户端本地防重，不是 Core
// 账务的原子性或幂等承诺。记录里不存令牌、Base64 或签名 URL。

var errImageGenerationTurnSpent = errors.New("本回合已经提交过一次图片生成")

const imageGenerationMsgInterrupted = "上次生成过程中断，结果无法确认，可能已生成或计费；不会自动重新生成"

type imageGenerationLedger struct {
	Version      int                            `json:"version"`
	GenerationID string                         `json:"generationId"`
	SessionID    string                         `json:"sessionId"`
	TurnID       string                         `json:"turnId"`
	QuestionID   string                         `json:"questionId"`
	ToolUseID    string                         `json:"toolUseId"`
	ScopeID      string                         `json:"scopeId"`
	Model        string                         `json:"model"`
	Size         string                         `json:"size"`
	Status       string                         `json:"status"`
	Result       *appwire.ImageGenerationResult `json:"result,omitempty"`
	RequestID    string                         `json:"requestId,omitempty"`
	// SubmissionStatus 只记录客户端是否进入过生成 HTTP Do；submitted 本身不代表已发送。
	SubmissionStatus string    `json:"submissionStatus,omitempty"`
	AccountingStatus string    `json:"accountingStatus,omitempty"`
	SubmittedAt      time.Time `json:"submittedAt,omitzero"`
	UpdatedAt        time.Time `json:"updatedAt,omitzero"`
}

func normalizeAccountingStatus(s string) string {
	switch s {
	case "verified", "ambiguous", "observed_mismatch":
		return s
	default:
		return "unverified"
	}
}

// normalizeSubmissionStatus 对历史账本保持保守：缺失、非法或无法证明的值均为 unknown。
func normalizeSubmissionStatus(s string) string {
	switch s {
	case appwire.ImageGenerationSubmissionNotSubmitted,
		appwire.ImageGenerationSubmissionAttempted:
		return s
	default:
		return appwire.ImageGenerationSubmissionUnknown
	}
}

func finalImageGenerationStatus(s string) bool {
	switch s {
	case "succeeded", "failed", "unknown", "cancelled", "save_failed":
		return true
	}
	return false
}

// finalized 把 result 记为本 generation 的结局。人工记下的记账观察保持不动：
// 文件或 HTTP 结果永不升级／覆盖它。
func (rec imageGenerationLedger) finalized(result appwire.ImageGenerationResult) imageGenerationLedger {
	rec.Status = result.Status
	if result.RequestID != "" {
		rec.RequestID = result.RequestID
	}
	// 账本中已有的可信提交边界优先；只有初始 unknown 才接受本次宿主明确结论。
	currentSubmission := normalizeSubmissionStatus(rec.SubmissionStatus)
	incomingSubmission := normalizeSubmissionStatus(result.SubmissionStatus)
	if currentSubmission == appwire.ImageGenerationSubmissionUnknown && incomingSubmission != appwire.ImageGenerationSubmissionUnknown {
		currentSubmission = incomingSubmission
	}
	rec.SubmissionStatus = currentSubmission
	rec.AccountingStatus = normalizeAccountingStatus(rec.AccountingStatus)
	result.AccountingStatus = rec.AccountingStatus
	result.RequestID = rec.RequestID
	result.SubmissionStatus = rec.SubmissionStatus
	rec.Result = &result
	rec.UpdatedAt = time.Now().UTC()
	return rec
}

// effectiveResult 是记录此刻的含义。已提交却无终态（进程中断或仍在写）视为
// unknown：可能已生成并计费，绝不再提交。
func (rec imageGenerationLedger) effectiveResult() appwire.ImageGenerationResult {
	var out appwire.ImageGenerationResult
	if finalImageGenerationStatus(rec.Status) && rec.Result != nil {
		out = *rec.Result
		out.Status = rec.Status
	} else {
		out = appwire.ImageGenerationResult{Status: "unknown", Code: "unknown_outcome", Message: imageGenerationMsgInterrupted}
	}
	out.Version, out.GenerationID = 1, rec.GenerationID
	if out.Model == "" {
		out.Model = rec.Model
	}
	if out.Size == "" {
		out.Size = rec.Size
	}
	if out.Images == nil || out.Status != "succeeded" {
		out.Images = []appwire.ImageGenerationArtifact{}
	}
	out.RequestID = rec.RequestID
	out.SubmissionStatus = normalizeSubmissionStatus(rec.SubmissionStatus)
	out.AccountingStatus = normalizeAccountingStatus(rec.AccountingStatus)
	return out
}

func imageGenerationLedgerDir(sessionID string) (string, error) {
	if !validImageGenerationID(sessionID) {
		return "", errImageGenerationStorage
	}
	return filepath.Join(".runcode", "image-generation", "requests", sessionID), nil
}

func imageGenerationLedgerPath(sessionID, generationID string) (string, error) {
	dir, err := imageGenerationLedgerDir(sessionID)
	if err != nil || !validImageGenerationID(generationID) {
		return "", errImageGenerationStorage
	}
	return filepath.Join(dir, generationID+".json"), nil
}

// reserveImageGenerationTurn 持久占用本回合唯一一次尝试。
func reserveImageGenerationTurn(workspace, sessionID, turnID, generationID string) error {
	if !validImageGenerationID(sessionID) || !validImageGenerationID(turnID) || !validImageGenerationID(generationID) {
		return errImageGenerationStorage
	}
	rel := filepath.Join(".runcode", "image-generation", "turns", sessionID, turnID+".json")
	data, _ := json.Marshal(map[string]any{"version": imageGenerationStoreVersion, "turnId": turnID, "generationId": generationID, "reservedAt": time.Now().UTC()})
	err := createRootedFileExclusive(workspace, rel, data)
	if errors.Is(err, os.ErrExist) {
		return errImageGenerationTurnSpent
	}
	return err
}

func writeImageGenerationLedger(workspace string, rec imageGenerationLedger) error {
	path, err := imageGenerationLedgerPath(rec.SessionID, rec.GenerationID)
	if err != nil {
		return err
	}
	rec.Version = imageGenerationStoreVersion
	rec.SubmissionStatus = normalizeSubmissionStatus(rec.SubmissionStatus)
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = time.Now().UTC()
	}
	data, err := json.Marshal(rec)
	if err != nil || len(data) > imageGenerationSidecarLimit {
		return errImageGenerationStorage
	}
	return writeRootedFileAtomic(workspace, path, data)
}

func decodeImageGenerationLedger(data []byte, sessionID, generationID string) (imageGenerationLedger, error) {
	var rec imageGenerationLedger
	if json.Unmarshal(data, &rec) != nil || rec.Version != imageGenerationStoreVersion || rec.SessionID != sessionID || rec.GenerationID != generationID {
		return imageGenerationLedger{}, errImageGenerationStorage
	}
	rec.SubmissionStatus = normalizeSubmissionStatus(rec.SubmissionStatus)
	return rec, nil
}

func readImageGenerationLedger(workspace, sessionID, generationID string) (imageGenerationLedger, error) {
	path, err := imageGenerationLedgerPath(sessionID, generationID)
	if err != nil {
		return imageGenerationLedger{}, err
	}
	data, exists, err := readRootedFile(workspace, path, imageGenerationSidecarLimit)
	if err != nil {
		return imageGenerationLedger{}, err
	}
	if !exists {
		return imageGenerationLedger{}, os.ErrNotExist
	}
	return decodeImageGenerationLedger(data, sessionID, generationID)
}

// listImageGenerationLedgers 返回会话全部账本记录。损坏是错误，不能悄悄跳过。
func listImageGenerationLedgers(workspace, sessionID string) ([]imageGenerationLedger, error) {
	ids, err := imageGenerationLedgerIDs(workspace, sessionID)
	if err != nil {
		return nil, err
	}
	out := []imageGenerationLedger{}
	for _, generationID := range ids {
		rec, err := readImageGenerationLedger(workspace, sessionID, generationID)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

// imageGenerationLedgerIDs enumerates only valid names inside the workspace
// root. Callers choose their own read-error policy; the strict loader above
// continues to reject an incomplete ledger set.
func imageGenerationLedgerIDs(workspace, sessionID string) ([]string, error) {
	dir, err := imageGenerationLedgerDir(sessionID)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return nil, errImageGenerationStorage
	}
	defer func() { _ = root.Close() }()
	d, err := root.Open(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, errImageGenerationStorage
	}
	entries, err := d.ReadDir(-1)
	_ = d.Close()
	if err != nil {
		return nil, errImageGenerationStorage
	}
	out := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		generationID := strings.TrimSuffix(name, ".json")
		if !validImageGenerationID(generationID) {
			continue // atomic-write temporaries carry extra dots
		}
		out = append(out, generationID)
	}
	return out, nil
}
