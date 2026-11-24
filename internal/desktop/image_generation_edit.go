package desktop

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	appwire "github.com/wt68/runcode/internal/protocol"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
)

// SaveImageGenerationEdit 把画板合成的 PNG 存为同一 generation 目录下的新 edited-*.png。
// 原图不动；路径必须落在本会话工作区 生成图片/<generationId>/ 下已有文件。
func (a *App) SaveImageGenerationEdit(req appwire.SaveImageGenerationEditRequest) (appwire.ImageGenerationArtifact, error) {
	sessionID := strings.TrimSpace(req.SessionID)
	sourcePath := strings.TrimSpace(req.SourcePath)
	sourceRef := strings.TrimSpace(req.SourceReference)
	if sessionID == "" || sourcePath == "" || sourceRef == "" || strings.TrimSpace(req.PNGBase64) == "" {
		return appwire.ImageGenerationArtifact{}, wireError(errors.New("保存标注需要会话、源路径、来源引用与 PNG 数据"))
	}
	if a.mgr == nil {
		return appwire.ImageGenerationArtifact{}, wireError(errNoSession)
	}
	var out appwire.ImageGenerationArtifact
	// 经 withArtifact 进入同一来源 SessionOperation：核对引用身份与路径，写前／写后再验 permit。
	err := a.withArtifact(context.Background(), sourceRef, func(_ context.Context, access host.SessionAccess, entry *artifactAccess) error {
		if entry.ref.SessionID != sessionID {
			return errors.New("来源引用与会话不匹配")
		}
		want := filepath.ToSlash(strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(sourcePath, `\`, `/`)), "/"))
		got := filepath.ToSlash(strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(entry.ref.Path, `\`, `/`)), "/"))
		if !strings.EqualFold(want, got) {
			return errors.New("来源引用与源路径不匹配")
		}
		art, err := saveImageGenerationEditAt(access.Workspace, sourcePath, req.PNGBase64)
		if err != nil {
			return err
		}
		out = art
		return nil
	})
	if err != nil {
		return appwire.ImageGenerationArtifact{}, wireError(err)
	}
	return out, nil
}

// parseImageGenerationEditSource 只接受 生成图片/<generationId>/<fileName> 相对路径。
func parseImageGenerationEditSource(rel string) (genID, fileName string, ok bool) {
	rel = strings.TrimSpace(rel)
	if rel == "" || filepath.IsAbs(rel) || strings.ContainsAny(rel, `:`) || strings.Contains(rel, "..") {
		return "", "", false
	}
	cleaned := path.Clean("/" + strings.ReplaceAll(rel, `\`, `/`))
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "..") {
		return "", "", false
	}
	parts := strings.Split(cleaned, "/")
	if len(parts) != 3 || parts[0] != imageGenerationDir || !validImageGenerationID(parts[1]) {
		return "", "", false
	}
	fileName = parts[2]
	if fileName == "" || fileName == "." || fileName == ".." || strings.ContainsAny(fileName, `/\`) {
		return "", "", false
	}
	for _, r := range fileName {
		if r < 0x20 || r == 0x7f || (!unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '-' && r != '_') {
			return "", "", false
		}
	}
	return parts[1], fileName, true
}

func saveImageGenerationEditAt(workspace, sourcePath, pngBase64 string) (appwire.ImageGenerationArtifact, error) {
	genID, fileName, ok := parseImageGenerationEditSource(sourcePath)
	if !ok {
		return appwire.ImageGenerationArtifact{}, errors.New("只能标注本会话生成图片目录下的图片")
	}
	if workspace == "" {
		return appwire.ImageGenerationArtifact{}, errors.New("会话工作区不可用")
	}
	data, err := decodeImageEditPNG(pngBase64)
	if err != nil {
		return appwire.ImageGenerationArtifact{}, err
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return appwire.ImageGenerationArtifact{}, errors.New("PNG 图片无效")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return appwire.ImageGenerationArtifact{}, errors.New("PNG 图片无效")
	}
	if cfg.Width > appwire.ImageEditMaxEdge || cfg.Height > appwire.ImageEditMaxEdge {
		return appwire.ImageGenerationArtifact{}, fmt.Errorf("图片边长超过 %d 像素上限", appwire.ImageEditMaxEdge)
	}

	root, err := os.OpenRoot(workspace)
	if err != nil {
		return appwire.ImageGenerationArtifact{}, errors.New("无法打开工作区")
	}
	defer func() { _ = root.Close() }()

	srcRel := filepath.FromSlash(path.Join(imageGenerationDir, genID, fileName))
	if _, err := root.Stat(srcRel); err != nil {
		return appwire.ImageGenerationArtifact{}, errors.New("源图片不存在或不可读")
	}

	now := time.Now().UTC()
	outName := fmt.Sprintf("edited-%s%03d.png", now.Format("20060102-150405"), now.Nanosecond()/1e6)
	outRel := filepath.Join(filepath.FromSlash(path.Join(imageGenerationDir, genID)), outName)
	f, err := root.OpenFile(outRel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return appwire.ImageGenerationArtifact{}, errors.New("无法创建标注副本（可能重名），请重试")
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if errors.Join(writeErr, syncErr, closeErr) != nil {
		_ = root.Remove(outRel)
		return appwire.ImageGenerationArtifact{}, errors.New("写入标注副本失败")
	}
	return appwire.ImageGenerationArtifact{
		Path:     path.Join(imageGenerationDir, genID, outName),
		MIMEType: "image/png",
		Bytes:    int64(len(data)),
		Width:    cfg.Width,
		Height:   cfg.Height,
	}, nil
}

func decodeImageEditPNG(raw string) ([]byte, error) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, ","); i >= 0 && strings.Contains(strings.ToLower(s[:i]), "base64") {
		s = s[i+1:]
	}
	maxB64 := int(appwire.ImageEditMaxBytes/3*4) + 64
	if len(s) > maxB64 {
		return nil, fmt.Errorf("图片超过 %d 字节上限", appwire.ImageEditMaxBytes)
	}
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(s)
		if err != nil {
			return nil, errors.New("PNG 数据无效")
		}
	}
	if int64(len(data)) > appwire.ImageEditMaxBytes {
		return nil, fmt.Errorf("图片超过 %d 字节上限", appwire.ImageEditMaxBytes)
	}
	if !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		return nil, errors.New("只接受 PNG 图片")
	}
	return data, nil
}
