#!/usr/bin/env bash
# runcode-server 冒烟脚本（bash + curl，无其他依赖）。
#
# 前提：服务端已用真实 LLM 环境启动，否则 SendMessage 会以 turn:error 收尾——
#   RUNCODE_PROVIDER / RUNCODE_MODEL / RUNCODE_API_KEY（或 RUNCODE_BASE_URL）
#   go run . 或 ./runcode-server
#
# 用法：
#   SERVER=http://127.0.0.1:8787 TOKEN=<可选> ./scripts/smoke.sh
#
# 步骤：GetProtocolInfo → StartSession → 后台订阅 SSE → SendMessage →
#       展示信封流（等 turn:end / turn:error）→ CloseSession。
set -euo pipefail

SERVER="${SERVER:-http://127.0.0.1:8787}"
AUTH=()
[ -n "${TOKEN:-}" ] && AUTH=(-H "Authorization: Bearer $TOKEN")

# 本机代理（Clash/v2ray 的 ALL_PROXY/HTTP_PROXY）会把 loopback 请求送进代理
# 而得到 503——对目标服务器一律直连。
CURL=(curl --noproxy '*')

say() { printf '\n== %s ==\n' "$*"; }

say "GetProtocolInfo (GET，query 命令允许 GET)"
"${CURL[@]}" -fsS "${AUTH[@]}" "$SERVER/api/v1/rpc/GetProtocolInfo"
echo

say "StartSession"
resp=$("${CURL[@]}" -fsS "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d '{"workspace":"smoke"}' "$SERVER/api/v1/rpc/StartSession")
echo "$resp"
sid=$(printf '%s' "$resp" | sed -n 's/.*"sessionId":"\([^"]*\)".*/\1/p')
[ -n "$sid" ] || { echo "FATAL: no sessionId in response" >&2; exit 1; }
echo "sessionId=$sid"

say "Subscribe SSE (background curl -N)"
events=$(mktemp)
"${CURL[@]}" -NsS "${AUTH[@]}" "$SERVER/api/v1/sessions/$sid/events" >"$events" &
sse_pid=$!
cleanup() { kill "$sse_pid" 2>/dev/null || true; rm -f "$events"; }
trap cleanup EXIT
sleep 1

say "SendMessage (202：结果走 SSE)"
"${CURL[@]}" -fsS "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d "{\"sessionId\":\"$sid\",\"text\":\"用一句话介绍你自己\"}" \
  "$SERVER/api/v1/rpc/SendMessage"
echo

say "Envelope stream (等待 turn:end / turn:error，最长 120s)"
for _ in $(seq 120); do
  if grep -q '"event":"turn:end"\|"event":"turn:error"' "$events" 2>/dev/null; then
    break
  fi
  sleep 1
done
cat "$events"

say "CloseSession"
"${CURL[@]}" -fsS "${AUTH[@]}" -H 'Content-Type: application/json' \
  -d "{\"sessionId\":\"$sid\"}" "$SERVER/api/v1/rpc/CloseSession"
echo

say "smoke OK"
