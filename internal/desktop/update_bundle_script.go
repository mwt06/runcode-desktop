package desktop

// macInstallScript 的参数依次为：zip、当前 bundle、zip 顶层目录、sha256、bundle ID、
// 版本、原签名团队、架构、可选的已建暂存目录。所有数据走位置参数，不拼进 shell。
//
// 在目标同一卷上先完整解压/验签，再做两次 rename；第二次失败就恢复旧 bundle。
// 特权分支把 zip 复制到 root 私有目录后重新验哈希，避免缓存的检查/使用竞态。
// 旧版只在新版完整就位后清理；回滚失败则故意留下备份并打印路径，不把唯一旧版删掉。
const macInstallScript = `set -eu
PATH=/usr/bin:/bin:/usr/sbin:/sbin
export PATH
umask 077
archive=$1
target=$2
appname=$3
hash=$4
bundleid=$5
version=$6
team=$7
arch=$8
work=$9
if [ -z "$work" ]; then work=$(/usr/bin/mktemp -d "${target%/*}/.runcode-update.XXXXXX"); fi
committed=0
cleanup() {
    result=$?
    trap - EXIT HUP INT TERM
    if [ -d "$work/previous.app" ] && [ "$committed" = 0 ]; then
        if [ -e "$target" ] || [ -L "$target" ] || ! /bin/mv "$work/previous.app" "$target"; then
            echo "恢复旧版失败，备份保留在 $work/previous.app" >&2
            exit 1
        fi
    fi
    /bin/rm -rf "$work" || true
    exit "$result"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
[ -d "$target" ] && [ ! -L "$target" ] || { echo '当前应用路径已变化，拒绝替换' >&2; exit 1; }
/bin/cp "$archive" "$work/package.zip"
digest=$(/usr/bin/shasum -a 256 "$work/package.zip")
[ "${digest%% *}" = "$hash" ] || { echo '安装前校验失败，安装包已变化' >&2; exit 1; }
/usr/bin/ditto -x -k "$work/package.zip" "$work/unpacked"
new="$work/unpacked/$appname"
plist="$new/Contents/Info.plist"
[ -d "$new" ] && [ ! -L "$new" ] || exit 1
[ "$(/usr/bin/plutil -extract CFBundleIdentifier raw -o - "$plist")" = "$bundleid" ] || { echo '更新包产品不符' >&2; exit 1; }
[ "$(/usr/bin/plutil -extract CFBundleShortVersionString raw -o - "$plist")" = "$version" ] || { echo '更新包版本不符' >&2; exit 1; }
[ "$(/usr/bin/plutil -extract CFBundlePackageType raw -o - "$plist")" = APPL ] || { echo '更新包不是应用程序' >&2; exit 1; }
executable=$(/usr/bin/plutil -extract CFBundleExecutable raw -o - "$plist")
case "$executable" in ''|.|..|*/*) echo '无效的应用入口' >&2; exit 1;; esac
[ -f "$new/Contents/MacOS/$executable" ] && [ -x "$new/Contents/MacOS/$executable" ] || { echo '应用入口不可执行' >&2; exit 1; }
/usr/bin/lipo -verify_arch "$arch" "$new/Contents/MacOS/$executable"
/usr/bin/codesign --verify --deep --strict "$new"
/usr/sbin/spctl --assess --type execute "$new"
if [ -n "$team" ]; then
    signature=$(/usr/bin/codesign -d --verbose=4 "$new" 2>&1)
    printf '%s\n' "$signature" | /usr/bin/grep -F -x "TeamIdentifier=$team" >/dev/null || { echo '更新包签名团队不符' >&2; exit 1; }
fi
/bin/mv "$target" "$work/previous.app"
/bin/mv "$new" "$target"
committed=1
`

// 只由未提权的应用启动：open 保持登录用户身份，不能把更新后的 GUI 以 root 打开。
// 等单实例锁随旧进程退出而释放；超时不重复起窗口，诊断保留在 relaunch.log。
const macRelaunchScript = `exec >"$3" 2>&1
n=0
while kill -0 "$1" 2>/dev/null; do
    n=$((n+1))
    if [ "$n" -ge 300 ]; then echo '旧进程未退出，请手动重新打开应用'; exit 1; fi
    sleep 1
done
exec /usr/bin/open -n "$2"
`
