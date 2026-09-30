package desktop

// Wails 命令门面与宿主装配。更新状态及用例在 appupdate，平台安装器仍在本包，
// 通过不可变的安装请求接收文件/版本/哈希，不再读取更新服务内部状态。
import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/wt68/runcode/internal/appupdate"
	"github.com/wt68/runcode/internal/protocol"
)

const (
	updateCheckDelay = 8 * time.Second
	updateQuitDelay  = 800 * time.Millisecond
)

func updateEndpoint() string {
	base := strings.TrimRight(envOr("RUNCODE_UPDATE_BASE_URL", passportConfig().BridgeBaseURL), "/")
	return base + envOr("RUNCODE_UPDATE_PATH", "/api/app/releases/latest")
}

func updatePlatform() string { return updateOS + "/" + runtime.GOARCH }

func updateCacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(base) == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "runcode", "updates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建更新目录失败: %w", err)
	}
	return dir, nil
}

func newUpdaterFor(a *App) *appupdate.Service {
	return appupdate.New(appupdate.Options{
		Current: AppVersion(), Product: AppProduct(), Platform: updatePlatform(), Endpoint: updateEndpoint(),
		HTTP: passportHTTP(), CacheDir: updateCacheDir, FetchToFile: fetchToFile,
		Token: func() string {
			if a.tokens == nil || !a.tokens.LoggedIn() {
				return ""
			}
			tok, _ := a.tokens.Token()
			return tok
		},
		Emit:       func(info protocol.UpdateInfo) { a.sink.Emit(protocol.EventUpdate, info) },
		CanInstall: canLaunchInstaller(), AutoRestart: willAutoRestart(), InstallHint: installHint(),
		Install: func(req appupdate.InstallRequest) error { return a.runInstaller(req.File, req.Version, req.SHA256) },
		Reveal:  func(file string) error { return startAndReap(revealCommand(file)) },
		Quit:    a.quitSoon, Log: debugLog,
	})
}

// UpdateStatus returns the current update snapshot without network access.
func (a *App) UpdateStatus() protocol.UpdateInfo { return a.upd.UpdateStatus() }

// CheckUpdate checks for a release explicitly requested by the user.
func (a *App) CheckUpdate() (protocol.UpdateInfo, error) {
	info, err := a.upd.CheckUpdate()
	return info, wireError(err)
}

// DownloadUpdate downloads and verifies the selected release.
func (a *App) DownloadUpdate() (protocol.UpdateInfo, error) {
	info, err := a.upd.DownloadUpdate()
	return info, wireError(err)
}

// CancelUpdateDownload cancels an outstanding download or manifest request.
func (a *App) CancelUpdateDownload() protocol.UpdateInfo { return a.upd.CancelUpdateDownload() }

// InstallUpdate dispatches a verified package to the platform installer.
func (a *App) InstallUpdate() error { return wireError(a.upd.InstallUpdate()) }

// RevealUpdate opens the downloaded package's directory as a manual fallback.
func (a *App) RevealUpdate() error { return wireError(a.upd.RevealUpdate()) }

// NSIS needs the old process to exit; give its UI time to become visible first.
func (a *App) quitSoon() {
	go func() {
		time.Sleep(updateQuitDelay)
		if a.quit != nil {
			a.quit.Quit()
		}
	}()
}

func (a *App) reportLastInstall() {
	cleanStaleWatchers()
	a.upd.ReportLastInstall()
}

// Only host startup/login schedule background work; construction and service tests do not.
var autoUpdateEnabled = true

func (a *App) autoCheckUpdate(delay time.Duration) {
	if !autoUpdateEnabled {
		return
	}
	if delay > 0 {
		time.Sleep(delay)
	}
	a.upd.AutoCheck()
}
