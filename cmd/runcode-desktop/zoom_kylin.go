//go:build kylin

package main

// 麒麟上"字大、框不大"的修正：把 WebKitGTK 的**只放大文字**换成**整页均匀放大**。
//
// # 现象（2026-09-18，V10 SP1 真机，2160×1440，系统缩放 150%）
//
// 界面上的字比框大一号半：审批弹窗的按钮字折成两行（"本次/会话"），侧栏的对话标题只剩
// 三四个字，模型名被截断。量截图：560px 宽的弹窗里，16px 的标题每个字宽 24px——
// 文字是 1.5 倍，布局是 1 倍。
//
// # 原因
//
// WebKitGTK 在建 WebView 时把 `screenDPI() / 96` 设成了页面的**文字缩放**
// （WebKitWebView.cpp，webkitWebViewConstructed：`textScaleFactor = screenDPI() / 96.;
// setTextZoomFactor(textScaleFactor)`），screenDPI() 取自 gdk_screen_get_resolution，
// 也就是 Xft.dpi。麒麟的"150% 缩放"正是 GDK_SCALE=1 + Xft.dpi=144：GTK 自己的控件靠
// 字变大撑开，而网页里 px 写死的框撑不开。Chromium 系与 Windows 的 WebView2 在同样
// 的设置下都是整页放大，只有 WebKitGTK 这样。
//
// # 修法
//
// 页面缩放设成同一个倍数，文字缩放归 1——效果就是用户在系统里选的那个 150%。WebKit
// 的公开 API 做得到，但要绕一下：
//
//  1. set_zoom_level(z)：不管它顺手把文字缩放设成什么，页面缩放一定是 z；
//  2. zoom-text-only 开：回调 zoomTextOnlyChanged 把状态变成 (页面 1, 文字 = 原页面 z)；
//  3. zoom-text-only 关：同一个回调变成 (页面 = 原文字 z, 文字 1)。
//
// 第 2、3 步的语义是照 2.38.6 的源码逐字核对过的（zoomTextOnlyChanged）；故意不依赖
// set_zoom_level 对文字缩放的处理，那一段没能核对到。
//
// 倍数与 WebKit 自己算 textScaleFactor 用同一个来源（gdk_screen_get_resolution，取不到
// 再看 gtk-xft-dpi）。WebKit 还有一个"按显示器物理尺寸估 DPI"的兜底，那里我们不跟：
// 取不到 DPI 时 z=1，结果是整页 100%——字与框一致，只是没放大，好过字大框小。
//
// RUNCODE_WEBVIEW_ZOOM 可以覆盖倍数（比如设成 1 看原始布局），给现场排查留的口子。
//
// 只在 V10 这份外壳里做。V11 走 Wails v3（WebKitGTK 4.1），大概率有同样的问题，但没有
// 真机验证过，不盲改。
//
// 连带的一处在前端：无边框窗口的边缘拉伸靠 Wails v2 的 JS 拿 window.outerWidth 与
// clientX 比，而页面缩放后前者是设备像素、后者是 CSS 像素，右边与下边会失灵——见
// frontend/src/core/wails-v2-runtime.ts 里对 outerWidth/outerHeight 的修正。

/*
#cgo pkg-config: gtk+-3.0 webkit2gtk-4.0
#include <gtk/gtk.h>
#include <webkit2/webkit2.h>

static double runcode_zoom_override = 0;
static int runcode_zoom_tries = 0;

static WebKitWebView *runcode_find_webview(GtkWidget *w) {
	if (WEBKIT_IS_WEB_VIEW(w)) {
		return WEBKIT_WEB_VIEW(w);
	}
	if (!GTK_IS_CONTAINER(w)) {
		return NULL;
	}
	WebKitWebView *found = NULL;
	GList *children = gtk_container_get_children(GTK_CONTAINER(w));
	for (GList *l = children; l != NULL && found == NULL; l = l->next) {
		found = runcode_find_webview(GTK_WIDGET(l->data));
	}
	g_list_free(children);
	return found;
}

static WebKitWebView *runcode_main_webview(void) {
	WebKitWebView *found = NULL;
	GList *tops = gtk_window_list_toplevels();
	for (GList *l = tops; l != NULL && found == NULL; l = l->next) {
		found = runcode_find_webview(GTK_WIDGET(l->data));
	}
	g_list_free(tops);
	return found;
}

// 与 WebKit 的 screenDPI() 前两步同源，好让我们放大的倍数就是它放大文字的倍数。
static double runcode_desktop_scale(void) {
	double dpi = -1;
	GdkScreen *screen = gdk_screen_get_default();
	if (screen != NULL) {
		dpi = gdk_screen_get_resolution(screen);
	}
	if (dpi <= 0) {
		GtkSettings *settings = gtk_settings_get_default();
		gint xft = 0;
		if (settings != NULL) {
			g_object_get(settings, "gtk-xft-dpi", &xft, NULL);
		}
		if (xft > 0) {
			dpi = xft / 1024.0;
		}
	}
	if (dpi <= 0) {
		return 1.0;
	}
	return dpi / 96.0;
}

static void runcode_apply_zoom(WebKitWebView *view) {
	double z = runcode_zoom_override > 0 ? runcode_zoom_override : runcode_desktop_scale();
	// 夹一道：离谱的 DPI（坏掉的 Xft.dpi、远程桌面报的怪值）不该把界面放到看不见。
	if (z < 0.5) z = 0.5;
	if (z > 3.0) z = 3.0;
	WebKitSettings *settings = webkit_web_view_get_settings(view);
	webkit_settings_set_zoom_text_only(settings, FALSE);
	webkit_web_view_set_zoom_level(view, z);
	webkit_settings_set_zoom_text_only(settings, TRUE);
	webkit_settings_set_zoom_text_only(settings, FALSE);
	g_message("runcode: webview zoom %.2f (page), text zoom reset to 1", z);
}

static gboolean runcode_reapply_idle(gpointer data) {
	runcode_apply_zoom(WEBKIT_WEB_VIEW(data));
	g_object_unref(data);
	return G_SOURCE_REMOVE;
}

// 用户运行中改了系统缩放：WebKit 自己的观察者会把文字缩放按新旧 DPI 之比改掉。放到
// idle 里重来一遍，保证排在它后面。
static void runcode_on_dpi_changed(GObject *obj, GParamSpec *pspec, gpointer data) {
	g_idle_add(runcode_reapply_idle, g_object_ref(data));
}

static gboolean runcode_zoom_fix(gpointer data) {
	WebKitWebView *view = runcode_main_webview();
	if (view == NULL) {
		// 按 Wails v2 的时序 WebView 此时早已建好（NewFrontend 里建，OnStartup 在其后）。
		// 找不到只能是时序变了：再等一会儿，别就此放弃。
		if (++runcode_zoom_tries < 50) {
			g_timeout_add(100, runcode_zoom_fix, NULL);
		} else {
			g_warning("runcode: webview not found, zoom left as WebKit set it");
		}
		return G_SOURCE_REMOVE;
	}
	runcode_apply_zoom(view);
	GtkSettings *settings = gtk_settings_get_default();
	if (settings != NULL) {
		g_signal_connect_object(settings, "notify::gtk-xft-dpi", G_CALLBACK(runcode_on_dpi_changed), view, 0);
	}
	return G_SOURCE_REMOVE;
}

// 可从任意线程调：g_idle_add 把活交给 GTK 主循环。
static void runcode_schedule_zoom_fix(double override) {
	runcode_zoom_override = override;
	g_idle_add(runcode_zoom_fix, NULL);
}
*/
import "C"

import (
	"log"
	"os"
	"strconv"
	"strings"
)

// scheduleWebviewZoomFix 把"只放大文字"换成整页放大（见文件头）。OnStartup 里调：
// 此时 WebView 已经建好，而 GTK 主循环还没开始画第一帧。
func scheduleWebviewZoomFix() {
	override := 0.0
	if v := strings.TrimSpace(os.Getenv("RUNCODE_WEBVIEW_ZOOM")); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f <= 0 {
			log.Printf("runcode-desktop: 忽略无效的 RUNCODE_WEBVIEW_ZOOM=%q", v)
		} else {
			override = f
		}
	}
	C.runcode_schedule_zoom_fix(C.double(override))
}
