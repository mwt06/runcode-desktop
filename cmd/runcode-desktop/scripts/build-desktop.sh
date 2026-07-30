#!/usr/bin/env bash
#
# 按品牌打包桌面应用(Wails)。同一套代码内置多套品牌(见 frontend/src/core/brand.ts),
# 品牌在构建时选定:前端读 VITE_BRAND,Go 侧的 OS 窗口标题读 -ldflags 注入的 brandTitle,
# 而应用名/图标/macOS bundle 标识符来自 wails.json 与 build/ 下的打包资产。本脚本把这
# 四处一次配齐,避免"前端是智开、图标和 bundle 还是 XRUN"这类只在成品上才看得出的错配。
#
# 用法(在 cmd/runcode-desktop 下执行):
#   ./scripts/build-desktop.sh                          # 默认品牌(XRUN),当前平台
#   ./scripts/build-desktop.sh --brand zhikai           # 智开,当前平台
#   ./scripts/build-desktop.sh --brand zhikai --universal   # 智开,macOS 通用二进制(Intel+ARM)
#   ./scripts/build-desktop.sh --brand zhikai --zip     # 打完再压成可分发的 zip(macOS)
#   ./scripts/build-desktop.sh --test                   # 测试版:内含"上下文审核"等仅测试版功能
#
# --test 注入 internal/desktop.testBuild 标记(见 internal/desktop/testbuild.go):
# 设置页出现"上下文审核"开关,可落盘并查看每次发给模型的完整上下文。正式分发包
# 一律不带 --test。
#
# 环境变量(可选,仅 macOS 分发需要):
#   APPLE_SIGN_ID    代码签名身份,如 "Developer ID Application: Foo (TEAMID)";设了才签名
#   APPLE_KEYCHAIN_PROFILE  notarytool 的钥匙串配置名;与 APPLE_SIGN_ID 同时设置才做公证
#
# Wails 不能交叉编译(三平台 WebView 各不相同:Windows WebView2 / macOS WKWebView /
# Linux WebKitGTK),所以本脚本只能打「当前所在系统」的包 —— Mac 包必须在 Mac 上跑。
set -euo pipefail

cd "$(dirname "$0")/.."

BRAND=runcode
PLATFORM=""
DO_ZIP=0
EXTRA=""
TEST_BUILD=0

while [ $# -gt 0 ]; do
  case "$1" in
    --brand) BRAND="${2:-}"; shift 2 ;;
    --platform) PLATFORM="${2:-}"; shift 2 ;;
    --universal) PLATFORM="darwin/universal"; shift ;;
    --zip) DO_ZIP=1; shift ;;
    --clean) EXTRA="$EXTRA -clean"; shift ;;
    --test) TEST_BUILD=1; shift ;;
    -h|--help) sed -n '2,24p' "$0"; exit 0 ;;
    *) echo "未知参数: $1(用 --help 看用法)" >&2; exit 2 ;;
  esac
done

# 品牌配置:显示名(应用名/Finder/任务栏)、产物文件名、窗口标题。
# 新增品牌时在这里加一支 case,并在 frontend/src/core/brand.ts 的 BRANDS 里加同名条目。
case "$BRAND" in
  runcode)
    APP_NAME="XRUN"; OUT_NAME="XRUN"; WIN_TITLE="XRUN"; VITE_BRAND_VALUE="" ;;
  zhikai)
    APP_NAME="智开"; OUT_NAME="智开"; WIN_TITLE="智开"; VITE_BRAND_VALUE="zhikai" ;;
  *)
    echo "未知品牌: $BRAND(可用: runcode, zhikai)" >&2; exit 2 ;;
esac

OS="$(uname -s)"
case "$OS" in
  Darwin) HOST=macos ;;
  Linux)  HOST=linux ;;
  *)      HOST=windows ;;
esac

# 目标平台:--platform 指定则以它为准(Wails 不能交叉编译,所以正常应与 HOST 一致,
# 但 darwin/universal 这类写法要能解析出 darwin)。
TARGET="$HOST"
case "${PLATFORM%%/*}" in
  darwin)  TARGET=macos ;;
  linux)   TARGET=linux ;;
  windows) TARGET=windows ;;
esac

# Windows 的 -o 不会自动补扩展名:不带 .exe 就会产出一个没有扩展名、双击打不开的
# 文件。macOS 相反 —— 传裸名,Wails 自己包成 <名字>.app。
OUT_FILE="$OUT_NAME"
if [ "$TARGET" = windows ]; then
  OUT_FILE="$OUT_NAME.exe"
fi

# Linux 的 WebKitGTK 在新发行版上是 4.1,需要构建标签;CI 同此(见 .github/workflows/desktop.yml)。
if [ "$HOST" = linux ]; then
  EXTRA="$EXTRA -tags webkit2_41"
fi
if [ -n "$PLATFORM" ]; then
  EXTRA="$EXTRA -platform $PLATFORM"
fi

command -v wails >/dev/null 2>&1 || {
  echo "找不到 wails CLI。安装:go install github.com/wailsapp/wails/v2/cmd/wails@latest" >&2
  exit 1
}

# ---- 临时套用品牌资产,退出时一律还原 ------------------------------------------
# 打包资产(wails.json / 图标 / Info.plist)在仓库里保存的是默认品牌的版本,换品牌
# 打包时就地覆盖、构建完还原,这样工作区不会因为打了一次别的品牌就留下脏改动。
BACKUP_DIR="$(mktemp -d)"
RESTORED=0
restore() {
  [ "$RESTORED" = 1 ] && return 0
  RESTORED=1
  [ -f "$BACKUP_DIR/wails.json" ] && cp "$BACKUP_DIR/wails.json" wails.json
  [ -f "$BACKUP_DIR/appicon.png" ] && cp "$BACKUP_DIR/appicon.png" build/appicon.png
  [ -f "$BACKUP_DIR/Info.plist" ] && cp "$BACKUP_DIR/Info.plist" build/darwin/Info.plist
  rm -rf "$BACKUP_DIR"
}
trap restore EXIT INT TERM

cp wails.json "$BACKUP_DIR/wails.json"
[ -f build/appicon.png ] && cp build/appicon.png "$BACKUP_DIR/appicon.png"
[ -f build/darwin/Info.plist ] && cp build/darwin/Info.plist "$BACKUP_DIR/Info.plist"

# wails.json 的三处名字决定应用名、产物名与 macOS/Windows 包里的展示名。
# 用 sed 生成再替换(而不是 sed -i:BSD 与 GNU 的 -i 语义不同,macOS 上会写出 .bak)。
sed -e "s/\"name\": \"[^\"]*\"/\"name\": \"$APP_NAME\"/" \
    -e "s/\"outputfilename\": \"[^\"]*\"/\"outputfilename\": \"$OUT_NAME\"/" \
    -e "s/\"productName\": \"[^\"]*\"/\"productName\": \"$APP_NAME\"/" \
    wails.json > "$BACKUP_DIR/wails.new.json"
mv "$BACKUP_DIR/wails.new.json" wails.json

BRAND_DIR="build/brands/$BRAND"
# 品牌可以有自己的应用图标;没有就沿用 build/appicon.png —— 这也是当前智开的选择,
# 三个平台因此拿到同一张图标(Windows 版一直如此)。要单独换某品牌的图标,放一张
# 1024×1024 PNG 到 build/brands/<品牌>/appicon.png,Wails 自动生成 .icns/.ico。
if [ -f "$BRAND_DIR/appicon.png" ]; then
  cp "$BRAND_DIR/appicon.png" build/appicon.png
fi
if [ -f "$BRAND_DIR/Info.plist" ]; then
  cp "$BRAND_DIR/Info.plist" build/darwin/Info.plist
fi

# ---- 构建 ---------------------------------------------------------------------
LDFLAGS="-X main.brandTitle=$WIN_TITLE"
TEST_LABEL=""
if [ "$TEST_BUILD" = 1 ]; then
  LDFLAGS="$LDFLAGS -X github.com/wt68/runcode/internal/desktop.testBuild=1"
  TEST_LABEL="  [测试版]"
fi
echo "▶ 品牌=$BRAND  应用名=$APP_NAME  平台=${PLATFORM:-$HOST}$TEST_LABEL"
export VITE_BRAND="$VITE_BRAND_VALUE"
# shellcheck disable=SC2086 # EXTRA 是有意按空格拆成多个参数的
wails build $EXTRA -ldflags "$LDFLAGS" -o "$OUT_FILE"

# ---- macOS:签名与公证(都可选,未配置则跳过) ----------------------------------
APP_PATH="build/bin/$OUT_NAME.app"
if [ "$TARGET" = macos ] && [ -d "$APP_PATH" ]; then
  if [ -n "${APPLE_SIGN_ID:-}" ]; then
    echo "▶ 代码签名:$APPLE_SIGN_ID"
    # --deep 已被 Apple 弃用;--options runtime(强化运行时)是公证的前置条件。
    codesign --force --deep --options runtime --timestamp \
      --sign "$APPLE_SIGN_ID" "$APP_PATH"
    codesign --verify --strict --verbose=2 "$APP_PATH"

    if [ -n "${APPLE_KEYCHAIN_PROFILE:-}" ]; then
      echo "▶ 公证(notarytool,数分钟)"
      ditto -c -k --keepParent "$APP_PATH" "build/bin/$OUT_NAME-notarize.zip"
      xcrun notarytool submit "build/bin/$OUT_NAME-notarize.zip" \
        --keychain-profile "$APPLE_KEYCHAIN_PROFILE" --wait
      # 装订票据:装订后用户离线也能通过 Gatekeeper 校验。
      xcrun stapler staple "$APP_PATH"
      rm -f "build/bin/$OUT_NAME-notarize.zip"
    fi
  else
    echo "ℹ️  未设 APPLE_SIGN_ID:产物未签名。自用可右键「打开」绕过 Gatekeeper;"
    echo "    要分发给别人请配好签名与公证(见脚本头部注释)。"
  fi

  if [ "$DO_ZIP" = 1 ]; then
    # 必须用 ditto:zip 不保留 .app 里的符号链接与权限位,会破坏签名。
    ditto -c -k --keepParent "$APP_PATH" "build/bin/$OUT_NAME-macos.zip"
    echo "▶ 已打包:build/bin/$OUT_NAME-macos.zip"
  fi
fi

restore
if [ "$TARGET" = macos ]; then
  echo "✅ 完成:$APP_PATH"
else
  echo "✅ 完成:build/bin/$OUT_FILE"
fi
