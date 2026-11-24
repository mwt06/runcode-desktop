package desktop

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/wt68/runcode/internal/localusage"
	"github.com/wt68/runcode/internal/protocol"
	hostproto "gitlab.ouc-online.com.cn/aibase/agentloop/protocol"
)

// S09 用量查询只读取本机 SQLite。Bridge 仍负责模型、租户和会话业务，
// 但不再提供客户端用量查询入口，避免在线统计与本机采集口径分裂。
const (
	usageTimezone     = "Asia/Shanghai"
	usageMaxTextLen   = 256
	usageMaxSearchLen = 100
	usageMaxDimPage   = 100000
)

// usageLoc 固定 UTC+8。正式 Windows 包常用 -trimpath，不一定带 zoneinfo；
// time.LoadLocation("Asia/Shanghai") 失败时整页会误报「本机用量记录暂不可用」。
var usageLoc = time.FixedZone(usageTimezone, 8*3600)

// usageNow 可注入测试时钟；生产环境只用于固定查询范围和数据水位文案。
var usageNow = time.Now

type usageInstantRange struct{ start, end time.Time }

func validUsageText(s string, maxRunes int) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= maxRunes && !strings.ContainsFunc(s, unicode.IsControl)
}

func usageError(code, message string) error { return &hostproto.Error{Code: code, Message: message} }
func usageInvalidArgument(message string) error {
	return usageError(hostproto.ErrCodeInvalidArgument, message)
}
func usageNotLoggedIn() error {
	return usageError(hostproto.ErrCodeNotLoggedIn, "请先登录通行证后查看用量")
}
func usageUnavailable(_ string) error {
	return usageError(protocol.ErrCodeUsageUnavailable, "本机用量记录暂不可用，请稍后重试")
}

func usageUnavailableFor(err error) error {
	if err == nil {
		return usageUnavailable("")
	}
	reason := "本机用量库无法打开"
	switch {
	case errors.Is(err, localusage.ErrSchemaTooNew):
		reason = "本机用量库由更新版本创建，当前客户端无法安全读取"
	case errors.Is(err, localusage.ErrSchemaUnsupported):
		reason = "本机用量库版本不受支持"
	case errors.Is(err, localusage.ErrSchemaCorrupt):
		reason = "本机用量库已损坏"
	}
	return usageError(protocol.ErrCodeUsageStorage, reason+"；可重建本地统计（原文件会保留备份）")
}

// GetUsageOverview 返回当前登录账号与租户的本机汇总。
func (a *App) GetUsageOverview(req protocol.UsageQuery) (protocol.UsageOverview, error) {
	a.usageMu.RLock()
	defer a.usageMu.RUnlock()
	if a.usageStore == nil {
		return protocol.UsageOverview{}, wireError(usageUnavailableFor(a.usageInitErr))
	}
	out, err := a.localUsageOverview(req)
	return out, wireError(err)
}

// GetUsageRequests 返回固定本机快照中的一页调用记录。
func (a *App) GetUsageRequests(req protocol.UsageRequestsQuery) (protocol.UsageRequestsPage, error) {
	a.usageMu.RLock()
	defer a.usageMu.RUnlock()
	if a.usageStore == nil {
		return protocol.UsageRequestsPage{}, wireError(usageUnavailableFor(a.usageInitErr))
	}
	out, err := a.localUsageRequests(req)
	return out, wireError(err)
}

// GetUsageDimensions 返回本机已采集的平台模型维度；供应商维度首期不提供。
func (a *App) GetUsageDimensions(req protocol.UsageDimensionsQuery) (protocol.UsageDimensionsPage, error) {
	a.usageMu.RLock()
	defer a.usageMu.RUnlock()
	if a.usageStore == nil {
		return protocol.UsageDimensionsPage{}, wireError(usageUnavailableFor(a.usageInitErr))
	}
	out, err := a.localUsageDimensions(req)
	return out, wireError(err)
}

// GetLocalUsageTenants 只读本机 SQLite 中当前账号与 Bridge 关联过的历史租户，
// 并补上当前活动／会话租户（即使尚无采集记录），方便直接查看本机空统计。
// 它不访问 Bridge，也不切换聊天会话当前租户。
func (a *App) GetLocalUsageTenants() ([]protocol.PassportTenant, error) {
	a.usageMu.RLock()
	defer a.usageMu.RUnlock()
	if a.usageStore == nil {
		return nil, wireError(usageUnavailableFor(a.usageInitErr))
	}
	account, err := a.localUsageIdentity()
	if err != nil {
		return nil, wireError(err)
	}
	bridge := digestBridge(passportConfig().BridgeBaseURL)
	ids, err := a.usageStore.ListTenantIDs(context.Background(), account, bridge)
	if err != nil {
		return nil, wireError(usageUnavailable(""))
	}
	seen := make(map[string]bool, len(ids)+2)
	out := make([]protocol.PassportTenant, 0, len(ids)+2)
	add := func(id string) {
		id = strings.TrimSpace(id)
		if !validLocalTenant(id) || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, protocol.PassportTenant{ID: id, Name: id})
	}
	for _, id := range ids {
		add(id)
	}
	add(a.ActiveTenant())
	a.mu.Lock()
	live := a.livePassportTenant
	a.mu.Unlock()
	add(live)
	return out, nil
}
