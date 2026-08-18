#!/usr/bin/env bash
#
# 按品牌打包桌面应用(Wails v3)。同一套代码内置多套品牌(见 frontend/src/core/brand.ts),
# 品牌在构建时选定,本脚本把四处开关一次配齐,避免"前端是智开、图标和 bundle 还是 XRUN"
# 这类只在成品上才看得出的错配:
#
#   1. 前端          VITE_BRAND 环境变量
#   2. 窗口标题       -ldflags -X main.brandTitle=...  (经 LDFLAGS_EXTRA 传进 Taskfile)
#   3. 应用名/产物名   wails3 task ... APP_NAME=...
#   4. 打包元数据      build/config.yml + build/windows/info.json + build/darwin/Info.plist
#
# 用法(在 cmd/runcode-desktop 下执行):
#   ./scripts/build-desktop.sh                              # 默认品牌(XRUN),当前平台
#   ./scripts/build-desktop.sh --brand zhikai               # 智开,当前平台
#   ./scripts/build-desktop.sh --brand zhikai --universal   # 智开,macOS 通用二进制
#   ./scripts/build-desktop.sh --brand zhikai --zip         # 打完再压成可分发的 zip(macOS)
#   ./scripts/build-desktop.sh --test                       # 测试版:含"上下文审核"等仅测试版功能
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

while [ $# -gt 0 ]; do
  case "$1" in
    --brand) BRAND="${2:-}"; shift 2 ;;
    --platform) PLATFORM="${2:-}"; shift 2 ;;
    --universal) PLATFORM="darwin/universal"; shift ;;
    --zip) DO_ZIP=1; shift ;;
    --clean) DO_CLEAN=1; shift ;;
    --test) TEST_BUILD=1; shift ;;
    -h|--help) sed -n '2,30p' "$0"; exit 0 ;;
    *) echo "未知参数: $1(用 --help 看用法)" >&2; exit 2 ;;
  esac
done

# 品牌配置:显示名(应用名/访达/任务栏)、窗口标题、前端开关、bundle 标识符。
# 新增品牌时在这里加一支 case,并在 frontend/src/core/brand.ts 的 BRANDS 里加同名条目。
#
# BUNDLE_ID 必须每个品牌都不同:相同标识符会让 macOS 把两个品牌当成同一个应用,
# 偏好设置、通知授权与 Gatekeeper 记录互相覆盖。
case "$BRAND" in
  runcode)
    APP_NAME="XRUN"; WIN_TITLE="XRUN"; VITE_BRAND_VALUE=""; BUNDLE_ID="cn.ouconline.ai.xrun" ;;
  zhikai)
    APP_NAME="智开"; WIN_TITLE="智开"; VITE_BRAND_VALUE="zhikai"; BUNDLE_ID="cn.ouconline.ai.zhikai" ;;
  *)
    echo "未知品牌: $BRAND(可用: runcode, zhikai)" >&2; exit 2 ;;
esac

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

command -v wails3 >/dev/null 2>&1 || {
  echo "找不到 wails3 CLI。安装:" >&2
  echo "  go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.9" >&2
  exit 1
}

# ---- 临时套用品牌资产,退出时一律还原 ------------------------------------------
# 打包资产在仓库里保存的是默认品牌的版本,换品牌打包时就地覆盖、构建完还原,
# 这样工作区不会因为打了一次别的品牌就留下脏改动。
BACKUP_DIR="$(mktemp -d)"
RESTORED=0
BRANDED_FILES="build/config.yml build/windows/info.json build/appicon.png build/darwin/Info.plist"
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
LDFLAGS_EXTRA="-X main.brandTitle=$WIN_TITLE"
TEST_LABEL=""
if [ "$TEST_BUILD" = 1 ]; then
  LDFLAGS_EXTRA="$LDFLAGS_EXTRA -X github.com/wt68/runcode/internal/desktop.testBuild=1"
  TEST_LABEL="  [测试版]"
fi

# Linux 的 WebKitGTK 在新发行版上是 4.1,需要构建标签。
EXTRA_TAGS=""
if [ "$TARGET" = linux ]; then
  EXTRA_TAGS="webkit2_41"
fi

# 目标任务:Windows 出 exe 就够了;macOS 还要包成 .app(通用二进制走 package:universal)。
TASK="build"
if [ "$TARGET" = darwin ]; then
  TASK="package"
  [ "$PLATFORM" = "darwin/universal" ] && TASK="package:universal"
fi

if [ "$DO_CLEAN" = 1 ]; then
  rm -rf bin
fi

echo "▶ 品牌=$BRAND  应用名=$APP_NAME  平台=$TARGET  任务=$TASK$TEST_LABEL"
export VITE_BRAND="$VITE_BRAND_VALUE"
wails3 task "$TASK" \
  APP_NAME="$APP_NAME" \
  LDFLAGS_EXTRA="$LDFLAGS_EXTRA" \
  ${EXTRA_TAGS:+EXTRA_TAGS="$EXTRA_TAGS"}

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
else
  echo "✅ 完成:bin/$APP_NAME.exe"
fi
