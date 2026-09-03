#!/usr/bin/env bash
#
# 按品牌打包桌面应用(Wails v3)。同一套代码内置多套品牌(见 frontend/src/core/brand.ts),
# 品牌在构建时选定,本脚本把六处开关一次配齐,避免"前端是智开、图标和 bundle 还是 XRUN"
# 这类只在成品上才看得出的错配:
#
#   1. 前端          VITE_BRAND 环境变量
#   2. 窗口标题       -ldflags -X main.brandTitle=...  (经 LDFLAGS_EXTRA 传进 Taskfile)
#   3. 单实例锁       -ldflags -X main.brandID=...     (同上;共用一把锁会让品牌互相挡住启动)
#   4. 应用名/产物名   wails3 task ... APP_NAME=...
#   5. 打包元数据      build/config.yml + build/windows/info.json + build/darwin/Info.plist
#   6. 版本与产品标识  -ldflags -X internal/desktop.appVersion / .appProduct  (版本更新要用)
#
# 第 6 处的版本号**不是在这里定的**:它读自 build/config.yml 的 info.version。本脚本再把
# 它写进 Windows 的版本资源(info.json)、macOS 的 Info.plist 与 NSIS 安装包,并注入二进制。
# 发版时改那一处即可 —— 各处各写一遍的下场是"关于里写 0.2.0、添加删除程序里写 0.1.0、
# exe 属性里写 0.1.0.0"这种装完机才看得见的错配。
#
# 用法(在 cmd/runcode-desktop 下执行):
#   ./scripts/build-desktop.sh                              # 默认品牌(XRUN),当前平台
#   ./scripts/build-desktop.sh --brand zhikai               # 智开,当前平台
#   ./scripts/build-desktop.sh --brand zhikai --universal   # 智开,macOS 通用二进制
#   ./scripts/build-desktop.sh --brand zhikai --zip         # 打完再压成可分发的 zip(macOS)
#   ./scripts/build-desktop.sh --brand zhikai --installer   # 智开,连 Windows 安装包(NSIS)
#   ./scripts/build-desktop.sh --test                       # 测试版:含"上下文审核"等仅测试版功能
#   ./scripts/build-desktop.sh --local-engine               # 联动本地 ../agentloop(产物不可复现,勿发版)
#   ./scripts/build-desktop.sh --brand zhikai --installer   # (在 Linux 上)出 .deb,给银河麒麟 V11
#   ./scripts/build-desktop.sh --brand zhikai --installer --kylin  # 麒麟 V10(Wails v2)
#
# ---- Linux / 银河麒麟 ----------------------------------------------------------
# 两条互斥的路,因为 V10 与 V11 的 WebKit ABI 不同,不可能是同一个二进制:
#
#   V11:默认路径。它带 GTK4 与 WebKitGTK 2.44,Wails v3 直接支持。CI 上加 --gtk3
#       走 GTK3 + WebKitGTK 4.1(V11 两个 ABI 都带),因为编译机的 glibc 必须不比
#       麒麟新,而带 6.0 的发行版都比它新。
#
#   V10:--kylin。它只有 WebKitGTK 的 4.0 ABI,而 Wails v3 没有 4.0 的代码路径,
#       所以这条走 **Wails v2**(外壳在 main_kylin.go,-tags kylin)。整条链路不碰
#       wails3:前端普通 vite 构建,Go 一句 go build,装包交给 nfpm。
#       代价是单窗口(v2 没有多窗口),录音窗因此不建——而录音采集本来就只有
#       Windows 有,Linux 上不损失已有能力。
#       编译环境必须是 Ubuntu 20.04 一代(glibc 2.31,且只有它还带
#       libwebkit2gtk-4.0-dev)。
#
# --gtk3 走 GTK3 + WebKitGTK 4.1 的老路径;它在 Wails v3.1 会被移除,所以默认不开。
#
# Linux 上应用名会自动换成 ASCII 的产品标识(xrun / zhikai):它同时是 deb 包名、
# 可执行文件名与 .desktop 文件名,而 deb 包名规范不接受中文。桌面菜单里显示的仍是
# 中文,那个走 .desktop 的 Name 字段。
#
# --installer 出 NSIS 安装包(仅 Windows;macOS 走 .app + --zip)。需要 makensis:
#   winget install --id NSIS.NSIS -e
# 默认装到 C:\Program Files\Ouc\desk_agent(要管理员;目录定在 build/windows/nsis/project.nsi
# 的 InstallDir)。加 --install-scope user 改成装进用户目录、免 UAC。
#
# --test 注入 internal/desktop.testBuild 标记(见 internal/desktop/testbuild.go):
# 设置页出现"上下文审核"开关,可落盘并查看每次发给模型的完整上下文。正式分发包
# 一律不带 --test。
#
# 环境变量(可选,仅 macOS 分发需要):
#   APPLE_SIGN_ID           代码签名身份,如 "Developer ID Application: Foo (TEAMID)";设了才签名
#   APPLE_KEYCHAIN_PROFILE  notarytool 的钥匙串配置名;与 APPLE_SIGN_ID 同时设置才做公证
#
# Wails 不能交叉编译(三平台 WebView 各不相同:Windows WebView2 / macOS WKWebView /
# Linux WebKitGTK),所以本脚本只能打「当前所在系统」的包 —— Mac 包必须在 Mac 上跑。
#
# ---- 从 v2 迁到 v3 改了什么 ---------------------------------------------------
# v2 的 `wails build` 一条命令包办一切,v3 换成 go-task 驱动(wails3 task)。三处差异
# 会咬人,都已在本脚本里处理:
#
#   · 产物目录从 build/bin/ 变成 bin/
#   · wails.json 变成 build/config.yml,且 **v3 不做模板替换** —— v2 时代
#     build/windows/info.json 与 build/darwin/Info.plist 里写的 {{.Info.*}} 由打包器
#     渲染,v3 是原样拷贝(build/darwin/Taskfile.yml 的 create:app:bundle 就是一句 cp)。
#     那些文件现在存字面量,品牌切换靠本脚本就地替换。
#   · -ldflags 写死在 build/windows/Taskfile.yml 的 BUILD_FLAGS 里,要追加 -X 注入得走
#     那里预留的 LDFLAGS_EXTRA 变量 —— 整个覆盖 BUILD_FLAGS 会把
#     -tags production/-trimpath/-H windowsgui 一起抄一遍,抄漏一个就是成品悄悄降级。
set -euo pipefail

cd "$(dirname "$0")/.."

BRAND=runcode
PLATFORM=""
DO_ZIP=0
DO_CLEAN=0
TEST_BUILD=0
DO_INSTALLER=0
INSTALL_SCOPE=machine
LOCAL_ENGINE=0
LINUX_GTK3=0
KYLIN10=0

while [ $# -gt 0 ]; do
  case "$1" in
    --brand) BRAND="${2:-}"; shift 2 ;;
    --platform) PLATFORM="${2:-}"; shift 2 ;;
    --universal) PLATFORM="darwin/universal"; shift ;;
    --zip) DO_ZIP=1; shift ;;
    --clean) DO_CLEAN=1; shift ;;
    --test) TEST_BUILD=1; shift ;;
    --installer) DO_INSTALLER=1; shift ;;
    --install-scope) INSTALL_SCOPE="${2:-}"; shift 2 ;;
    --local-engine) LOCAL_ENGINE=1; shift ;;
    --gtk3) LINUX_GTK3=1; shift ;;
    --kylin) KYLIN10=1; shift ;;
    -h|--help) sed -n '2,62p' "$0"; exit 0 ;;
    *) echo "未知参数: $1(用 --help 看用法)" >&2; exit 2 ;;
  esac
done

# 品牌配置:显示名(应用名/访达/任务栏)、窗口标题、前端开关、bundle 标识符。
# 新增品牌时在这里加一支 case,并在 frontend/src/core/brand.ts 的 BRANDS 里加同名条目。
#
# BUNDLE_ID 必须每个品牌都不同:相同标识符会让 macOS 把两个品牌当成同一个应用,
# 偏好设置、通知授权与 Gatekeeper 记录互相覆盖。
# PRODUCT 是更新清单里的产品标识(GET .../releases/latest?product=...)。必须每个品牌
# 都不同,而且**只能用 ASCII**:它要进 URL 查询参数与本地安装包的文件名。给智开推
# XRUN 的安装包等于把用户的应用换成另一个牌子。
case "$BRAND" in
  runcode)
    APP_NAME="XRUN"; WIN_TITLE="XRUN"; VITE_BRAND_VALUE=""; BUNDLE_ID="cn.ouconline.ai.xrun"; PRODUCT="xrun" ;;
  zhikai)
    APP_NAME="智开"; WIN_TITLE="智开"; VITE_BRAND_VALUE="zhikai"; BUNDLE_ID="cn.ouconline.ai.zhikai"; PRODUCT="zhikai" ;;
  *)
    echo "未知品牌: $BRAND(可用: runcode, zhikai)" >&2; exit 2 ;;
esac

# Linux 上应用名必须是 ASCII:它同时当二进制名、deb 包名与 .desktop 的文件名,而
# deb 包名规范只允许小写字母数字加 -+. ——「智开」放进去直接是非法包名。中文改走
# DISPLAY_NAME,只出现在桌面菜单里显示的那一行。
DISPLAY_NAME="$APP_NAME"

OS="$(uname -s)"
case "$OS" in
  Darwin) HOST=darwin ;;
  Linux)  HOST=linux ;;
  *)      HOST=windows ;;
esac

TARGET="$HOST"
case "${PLATFORM%%/*}" in
  darwin)  TARGET=darwin ;;
  linux)   TARGET=linux ;;
  windows) TARGET=windows ;;
esac

if [ "$KYLIN10" = 1 ]; then
  TARGET=linux
fi
if [ "$TARGET" = linux ]; then
  APP_NAME="$PRODUCT"
fi

# 麒麟 V10 那条走 Wails v2，整条链路不碰 wails3——而且它的 CLI 自己就要
# webkit2gtk-4.1，在 V10 的编译环境里根本装不上，所以这里必须跳过检查。
if [ "$KYLIN10" != 1 ]; then
  command -v wails3 >/dev/null 2>&1 || {
    echo "找不到 wails3 CLI。安装:" >&2
    echo "  go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.9" >&2
    exit 1
  }
fi

# ---- 临时套用品牌资产,退出时一律还原 ------------------------------------------
# 打包资产在仓库里保存的是默认品牌的版本,换品牌打包时就地覆盖、构建完还原,
# 这样工作区不会因为打了一次别的品牌就留下脏改动。
BACKUP_DIR="$(mktemp -d)"
RESTORED=0
BRANDED_FILES="build/config.yml build/windows/info.json build/appicon.png build/darwin/Info.plist build/windows/nsis/project.nsi build/linux/nfpm/nfpm.yaml"
restore() {
  [ "$RESTORED" = 1 ] && return 0
  RESTORED=1
  for f in $BRANDED_FILES; do
    b="$BACKUP_DIR/$(echo "$f" | tr '/' '_')"
    [ -f "$b" ] && cp "$b" "$f"
  done
  rm -rf "$BACKUP_DIR"
}
trap restore EXIT INT TERM

for f in $BRANDED_FILES; do
  [ -f "$f" ] && cp "$f" "$BACKUP_DIR/$(echo "$f" | tr '/' '_')"
done

# 用 sed 生成再替换(而不是 sed -i:BSD 与 GNU 的 -i 语义不同,macOS 上会写出 .bak)。
subst() { # subst <文件> <sed 表达式...>
  local file="$1"; shift
  sed "$@" "$file" > "$BACKUP_DIR/tmp.out"
  mv "$BACKUP_DIR/tmp.out" "$file"
}

# 打包元数据:应用名与 bundle 标识符。
subst build/config.yml \
  -e "s/^\(  productName: \).*/\1\"$APP_NAME\"/" \
  -e "s/^\(  productIdentifier: \).*/\1\"$BUNDLE_ID\"/"

# Windows 版本资源(右键属性 → 详细信息)。
subst build/windows/info.json \
  -e "s/\"ProductName\": \"[^\"]*\"/\"ProductName\": \"$APP_NAME\"/" \
  -e "s/\"FileDescription\": \"[^\"]*\"/\"FileDescription\": \"$APP_NAME\"/"

# Linux 包的元数据。nfpm.yaml 里出现的每一个 xrun 都是同一个概念——ASCII 产品标识:
# deb 包名、/usr/local/bin 下的可执行文件名、图标名、.desktop 文件名。它们必须一致,
# 不一致的表现是装完之后菜单里有图标、点下去启动不了。
if [ "$TARGET" = linux ]; then
  subst build/linux/nfpm/nfpm.yaml -e "s/\bxrun\b/$PRODUCT/g"
fi

BRAND_DIR="build/brands/$BRAND"
# 品牌可以有自己的应用图标;没有就沿用 build/appicon.png —— 这也是当前智开的选择,
# 三个平台因此拿到同一张图标。要单独换某品牌的图标,放一张 1024×1024 PNG 到
# build/brands/<品牌>/appicon.png。
if [ -f "$BRAND_DIR/appicon.png" ]; then
  cp "$BRAND_DIR/appicon.png" build/appicon.png
fi
if [ -f "$BRAND_DIR/Info.plist" ]; then
  cp "$BRAND_DIR/Info.plist" build/darwin/Info.plist
fi

# ---- 构建 ---------------------------------------------------------------------
# brandID 是单实例锁的标识符,必须跟着品牌走。共用一把锁的后果是:XRUN 开着的时候
# 双击智开什么都不发生(进程发现锁被占就以 0 退出,无窗口无报错),反之亦然。
# ---- 引擎按已发布 tag 解析,不吃本地 checkout ------------------------------------
# 默认 GOWORK=off。仓库根的 go.work 把 ../agentloop 联动进来,那对改引擎时的开发很好用,
# 但**发版时是个陷阱**:构建会把本地 checkout 的代码(可能是未提交的、任何 tag 里都没有的)
# 编进成品,而成品看不出这件事。真出过一次——0.1.2 的包里带着只存在于一台机器上的引擎
# 改动,谁也复现不出来。CLAUDE.md 写的"CI/发布链路一律 GOWORK=off"就是这条,这里把它
# 从约定变成脚本保证。
#
# GOPRIVATE 必须一起设:引擎在内网 GitLab,不设的话 go 会去公网 proxy 找它然后失败。
#
# 要临时联动本地引擎(比如验证一个还没打 tag 的引擎改动),加 --local-engine——
# 那样出来的包不可复现,别拿去发版。
if [ "$LOCAL_ENGINE" = 1 ]; then
  echo "⚠️  --local-engine:走 go.work 联动 ../agentloop,产物不可复现,勿用于发版"
else
  export GOWORK=off
  export GOPRIVATE="${GOPRIVATE:-gitlab.ouc-online.com.cn}"
fi

# 版本号从打包元数据里读(唯一事实来源,见脚本头部第 6 条)。读不到就停:一个没有版本号
# 的构建在"检查更新"面前只会一直自称 0.0.0-dev,于是每次都提示有新版。
cfg_get() { sed -n "s/^  $1: \"\(.*\)\"[[:space:]]*$/\1/p" build/config.yml | head -1; }
APP_VERSION="$(cfg_get version)"
[ -n "$APP_VERSION" ] || { echo "读不出 build/config.yml 的 info.version" >&2; exit 1; }

# 版本号同样要落进 Windows 版本资源与 macOS 包清单。这两份文件存的是字面量(v3 不做模板
# 替换,见脚本头部),仓库里那份随手就落后于 config.yml——真出过:config.yml 已经是
# 0.1.3.4,exe 右键「详细信息」还写 0.1.0.0。这里按 config.yml 就地覆盖,构建完还原。
#
# Windows 的 fixed 版本(file_version / product_version)必须是四段纯数字:去掉预发布
# 后缀,不足四段补 0,多于四段截断。macOS 的 CFBundleVersion 也只认数字和点。
APP_CORE_VERSION="${APP_VERSION%%-*}"
WIN_FILE_VERSION="$(printf '%s' "$APP_CORE_VERSION" | awk -F. '{
  for (i = 1; i <= 4; i++) {
    n = (i <= NF && $i ~ /^[0-9]+$/) ? $i + 0 : 0
    printf "%s%d", (i > 1 ? "." : ""), n
  }
}')"
subst build/windows/info.json \
  -e "s/\"file_version\": \"[^\"]*\"/\"file_version\": \"$WIN_FILE_VERSION\"/" \
  -e "s/\"product_version\": \"[^\"]*\"/\"product_version\": \"$WIN_FILE_VERSION\"/" \
  -e "s/\"ProductVersion\": \"[^\"]*\"/\"ProductVersion\": \"$APP_VERSION\"/" \
  -e "s/\"FileVersion\": \"[^\"]*\"/\"FileVersion\": \"$APP_VERSION\"/"
# plist 里 <key> 与 <string> 分两行:命中键那行后用 n 跳到下一行再替换。
subst build/darwin/Info.plist \
  -e "/<key>CFBundleVersion<\/key>/{n;s|<string>[^<]*</string>|<string>$APP_CORE_VERSION</string>|;}" \
  -e "/<key>CFBundleShortVersionString<\/key>/{n;s|<string>[^<]*</string>|<string>$APP_CORE_VERSION</string>|;}"

# nfpm 按 ${APP_VERSION} 展开环境变量取版本号（同它自带的 arch: ${GOARCH}），
# 所以这里要导出，否则 deb 的版本会是字面量 ${APP_VERSION}。
export APP_VERSION

DESKTOP_PKG="github.com/wt68/runcode/internal/desktop"
LDFLAGS_EXTRA="-X main.brandTitle=$WIN_TITLE -X main.brandID=$BUNDLE_ID"
LDFLAGS_EXTRA="$LDFLAGS_EXTRA -X $DESKTOP_PKG.appVersion=$APP_VERSION -X $DESKTOP_PKG.appProduct=$PRODUCT"
TEST_LABEL=""
if [ "$TEST_BUILD" = 1 ]; then
  LDFLAGS_EXTRA="$LDFLAGS_EXTRA -X github.com/wt68/runcode/internal/desktop.testBuild=1"
  TEST_LABEL="  [测试版]"
fi

# Linux 的 GTK/WebKit 路径。
#
# 默认走 Wails v3 的默认路径:GTK4 + WebKitGTK 6.0(麒麟 V11 带的是 2.44,正好)。
# webkit2_41 是 **v2 时代**的标记,v3 里根本不存在——留着它只是白加一个没人认的
# tag,真正的老路径开关叫 gtk3(GTK3 + WebKitGTK 4.1),那条在 v3.1 会被移除。
#
# 麒麟 V10 两条都不行:它只有 WebKitGTK 2.28(4.0 ABI),而 v3 没有 4.0 的代码路径。
EXTRA_TAGS=""
if [ "$TARGET" = linux ] && [ "$LINUX_GTK3" = 1 ]; then
  EXTRA_TAGS="gtk3"
fi

# 目标任务:Windows 默认出 exe 就够了(--installer 才做 NSIS 安装包);macOS 一律要包成
# .app(通用二进制走 package:universal)。
TASK="build"
if [ "$TARGET" = darwin ]; then
  TASK="package"
  [ "$PLATFORM" = "darwin/universal" ] && TASK="package:universal"
elif [ "$TARGET" = linux ] && [ "$DO_INSTALLER" = 1 ]; then
  # 麒麟桌面版是 deb 系(V10/V11 都基于 Ubuntu 血统),所以只出 deb;要 rpm/AppImage
  # 的话 build/linux/Taskfile.yml 里有现成的 create:rpm / create:appimage。
  TASK="linux:create:deb"
elif [ "$DO_INSTALLER" = 1 ]; then
  TASK="package"
fi

# ---- Windows 安装包:makensis 与品牌定义 ---------------------------------------
if [ "$TARGET" = windows ] && [ "$DO_INSTALLER" = 1 ]; then
  case "$INSTALL_SCOPE" in
    user|machine) ;;
    *) echo "未知 --install-scope: $INSTALL_SCOPE(可用: machine, user)" >&2; exit 2 ;;
  esac

  # NSIS 的安装器不改 PATH,而 Taskfile 里就是一句裸 makensis。装了却找不到会以
  # 一句 "executable file not found" 结束,看不出缺的是什么,所以这里自己找、
  # 自己把目录塞进 PATH——不去动系统的环境变量。
  if ! command -v makensis >/dev/null 2>&1; then
    for d in "/c/Program Files (x86)/NSIS" "/c/Program Files/NSIS" "$PROGRAMFILES/NSIS" "${PROGRAMFILES:-}/NSIS"; do
      if [ -x "$d/makensis.exe" ]; then PATH="$d:$PATH"; export PATH; break; fi
    done
  fi
  command -v makensis >/dev/null 2>&1 || {
    echo "找不到 makensis(NSIS)。安装:" >&2
    echo "  winget install --id NSIS.NSIS -e" >&2
    exit 1
  }

  # 安装包的元信息走 project.nsi 顶部的 !define,不用 makensis 的 -D 命令行参数。
  #
  # 两个原因。其一,wails_tools.nsh 是 wails 生成的,里面那几个默认值还是模板占位
  # ("My Company" / "My Product"),而 nsis 打包这条链路**不会**重新生成它——照它走
  # 出来的安装包会自称 My Product。其二,产品名是中文:命令行参数要经过控制台代码页,
  # 在这台机器上是 GBK,一路传下去很容易变成乱码,而 .nsi 文件可以带 UTF-8 BOM 让
  # makensis 明确按 UTF-8 读。两处 !ifndef 都在 wails_tools.nsh 里,先定义的赢。
  #
  # UNINST_KEY_NAME 必须跟着品牌走(默认是 公司名+产品名 拼出来的)。共用一个键的
  # 后果和共用 bundle 标识符一样:两个品牌在「添加或删除程序」里是同一条,装了一个
  # 另一个的卸载入口就被顶掉。这里直接用 bundle 标识符,天然每个品牌不同。
  NSIS_COMPANY="$(cfg_get companyName)"
  NSIS_VERSION="$APP_VERSION"
  NSIS_COPYRIGHT="$(cfg_get copyright)"
  {
    printf '\xEF\xBB\xBF'   # UTF-8 BOM:makensis 靠它认出编码,没有它中文按系统代码页读
    printf 'Unicode true\n\n'
    printf '# 以下 !define 由 scripts/build-desktop.sh 按品牌生成,构建完会还原。\n'
    printf '!define INFO_PROJECTNAME    "%s"\n' "$APP_NAME"
    printf '!define INFO_PRODUCTNAME    "%s"\n' "$APP_NAME"
    printf '!define INFO_COMPANYNAME    "%s"\n' "$NSIS_COMPANY"
    printf '!define INFO_PRODUCTVERSION "%s"\n' "$NSIS_VERSION"
    printf '!define INFO_COPYRIGHT      "%s"\n' "$NSIS_COPYRIGHT"
    printf '!define PRODUCT_EXECUTABLE  "%s.exe"\n' "$APP_NAME"
    printf '!define UNINST_KEY_NAME     "%s"\n\n' "$BUNDLE_ID"
    tail -n +2 "$BACKUP_DIR/build_windows_nsis_project.nsi"   # 原文第一行就是 Unicode true
  } > "$BACKUP_DIR/nsi.out"
  mv "$BACKUP_DIR/nsi.out" build/windows/nsis/project.nsi
  SCOPE_ARG="INSTALL_SCOPE=$INSTALL_SCOPE"
fi
SCOPE_ARG="${SCOPE_ARG:-}"

if [ "$DO_CLEAN" = 1 ]; then
  rm -rf bin
fi

echo "▶ 品牌=$BRAND  应用名=$APP_NAME  版本=$APP_VERSION  产品=$PRODUCT  平台=$TARGET  任务=$TASK$TEST_LABEL"
export VITE_BRAND="$VITE_BRAND_VALUE"

# ---- 麒麟 V10：Wails v2 那条路，整条链路都不碰 wails3 --------------------------
#
# 为什么单独一条：V10 的 WebKitGTK 只有 4.0 ABI，只能用 Wails v2（见 main_kylin.go）。
# 而 wails3 的 CLI 自己就要 webkit2gtk-4.1 才编得起来，在这种机器上装都装不上。
# 好在 v2 这条根本不需要任何 Wails CLI：前端是普通的 vite 构建，Go 那边就是一句
# go build -tags kylin，装包交给 nfpm。三步都直接调，反而比走 task 少一层。
if [ "$KYLIN10" = 1 ]; then
  echo "▶ 麒麟 V10 模式：Wails v2 + webkit2gtk-4.0，单窗口"

  (cd frontend && npm ci && npm run build)

  # -tags kylin 选中 main_kylin.go 那份外壳；CGO 必须开，WebKitGTK 的绑定是 cgo。
  CGO_ENABLED=1 go build -tags kylin -trimpath -buildvcs=false \
    -ldflags "-w -s $LDFLAGS_EXTRA" -o "bin/$APP_NAME"
  echo "▶ 已编译 bin/$APP_NAME"

  if [ "$DO_INSTALLER" = 1 ]; then
    # .desktop 手写而不是让 wails3 生成——那个生成器也在 CLI 里。字段与
    # build/linux/desktop 那份模板一致，Name 用中文显示名。
    mkdir -p build/linux
    cat > "build/linux/$APP_NAME.desktop" <<DESKTOP
[Desktop Entry]
Version=1.0
Name=$DISPLAY_NAME
Comment=AI 办公助手（桌面版）
Exec=/usr/local/bin/$APP_NAME %u
Terminal=false
Type=Application
Icon=$APP_NAME
Categories=Office;Utility;
StartupWMClass=$APP_NAME
DESKTOP
    command -v nfpm >/dev/null 2>&1 || go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest
    nfpm package -f build/linux/nfpm/nfpm.yaml -p deb -t bin/
    echo "▶ 已出 deb"
  fi

  restore
  echo "✅ 完成:bin/$APP_NAME"
  for f in bin/*.deb; do
    [ -f "$f" ] && echo "✅ 安装包:$f（麒麟 V10）"
  done
  exit 0
fi

wails3 task "$TASK" \
  APP_NAME="$APP_NAME" \
  DISPLAY_NAME="$DISPLAY_NAME" \
  LDFLAGS_EXTRA="$LDFLAGS_EXTRA" \
  ${EXTRA_TAGS:+EXTRA_TAGS="$EXTRA_TAGS"} \
  ${SCOPE_ARG:+"$SCOPE_ARG"}

# ---- macOS:签名与公证(都可选,未配置则跳过) ----------------------------------
# v3 的产物在 bin/ 而不是 v2 的 build/bin/。
APP_PATH="bin/$APP_NAME.app"
if [ "$TARGET" = darwin ] && [ -d "$APP_PATH" ]; then
  if [ -n "${APPLE_SIGN_ID:-}" ]; then
    echo "▶ 代码签名:$APPLE_SIGN_ID"
    # --deep 已被 Apple 弃用;--options runtime(强化运行时)是公证的前置条件。
    codesign --force --deep --options runtime --timestamp \
      --sign "$APPLE_SIGN_ID" "$APP_PATH"
    codesign --verify --strict --verbose=2 "$APP_PATH"

    if [ -n "${APPLE_KEYCHAIN_PROFILE:-}" ]; then
      echo "▶ 公证(notarytool,数分钟)"
      ditto -c -k --keepParent "$APP_PATH" "bin/$APP_NAME-notarize.zip"
      xcrun notarytool submit "bin/$APP_NAME-notarize.zip" \
        --keychain-profile "$APPLE_KEYCHAIN_PROFILE" --wait
      # 装订票据:装订后用户离线也能通过 Gatekeeper 校验。
      xcrun stapler staple "$APP_PATH"
      rm -f "bin/$APP_NAME-notarize.zip"
    fi
  else
    echo "ℹ️  未设 APPLE_SIGN_ID:产物未签名。自用可右键「打开」绕过 Gatekeeper;"
    echo "    要分发给别人请配好签名与公证(见脚本头部注释)。"
  fi

  if [ "$DO_ZIP" = 1 ]; then
    # 必须用 ditto:zip 不保留 .app 里的符号链接与权限位,会破坏签名。
    ditto -c -k --keepParent "$APP_PATH" "bin/$APP_NAME-macos.zip"
    echo "▶ 已打包:bin/$APP_NAME-macos.zip"
  fi
fi

restore
if [ "$TARGET" = darwin ]; then
  echo "✅ 完成:$APP_PATH"
elif [ "$TARGET" = linux ]; then
  echo "✅ 完成:bin/$APP_NAME"
  for f in bin/*.deb; do
    [ -f "$f" ] && echo "✅ 安装包:$f"
  done
else
  echo "✅ 完成:bin/$APP_NAME.exe"
  if [ "$DO_INSTALLER" = 1 ]; then
    # 安装包名里的 amd64 来自 project.nsi 的 OutFile,跟着构建架构走。
    for f in bin/"$APP_NAME"-*-installer.exe; do
      [ -f "$f" ] && echo "✅ 安装包:$f($INSTALL_SCOPE 范围)"
    done
  fi
fi
