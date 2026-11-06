#!/usr/bin/env bash
# CN3.5 服务端并发压测：构建 mercd35 + cn35load，用**独立的全新 state 目录**
# 起一个压测实例（不触碰 state/cn35 等玩家库），跑完自动停服并汇总。
#
#   tools/cn35_loadtest.sh                         默认 200 人 normal，稳态 3 分钟
#   tools/cn35_loadtest.sh -players 300 -duration 5m
#   tools/cn35_loadtest.sh -profile stress         无思考时间，测饱和吞吐/排队
#   KEEP_STATE=1 tools/cn35_loadtest.sh            保留压测库供 sqlite 排查
#   NO_REQLOG=1 tools/cn35_loadtest.sh             关闭 reqlog/event-log 对比日志开销
#
# 其余参数原样透传给 cn35load（见 go run ./server/cmd/cn35load -h）。
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
port="${LOAD_PORT:-18190}"
stamp="$(date +%Y%m%d-%H%M%S)"
work="$repo/output/loadtest/$stamp"
state="$work/state"
mkdir -p "$state"

echo "构建 -> $work"
(cd "$repo/server" && go build -trimpath -o "$work/mercd35" ./cmd/mercd35 && go build -trimpath -o "$work/cn35load" ./cmd/cn35load)

if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
  echo "端口 $port 已占用，设置 LOAD_PORT=其他端口" >&2; exit 1
fi

logargs=(-reqlog "$state/requests.ndjson" -event-log "$state/events.ndjson")
[[ "${NO_REQLOG:-}" == 1 ]] && logargs=(-reqlog "" -event-log "")

(cd "$repo/server" && exec "$work/mercd35" -addr "127.0.0.1:$port" -server-host "127.0.0.1:$port" \
  -assets assets/cn350 -cards assets/shared/cards -state "$state" "${logargs[@]}" \
  -remote-assets-scan 0) >"$work/server.log" 2>&1 &
srv=$!
cleanup() {
  kill "$srv" 2>/dev/null || true; wait "$srv" 2>/dev/null || true
  [[ -n "${mon:-}" ]] && kill "$mon" 2>/dev/null || true
  if [[ "${KEEP_STATE:-}" != 1 ]]; then rm -rf "$state"; fi
}
trap cleanup EXIT

for _ in $(seq 1 120); do
  curl -fsS "http://127.0.0.1:$port/mercd/health" >/dev/null 2>&1 && break
  kill -0 "$srv" 2>/dev/null || { echo "mercd35 启动失败："; tail -30 "$work/server.log"; exit 1; }
  sleep 0.5
done

# 每 5s 采样服务端 CPU/RSS
( while kill -0 "$srv" 2>/dev/null; do
    ps -o %cpu=,rss= -p "$srv" | awk -v t="$(date +%s)" '{printf "%s cpu=%s%% rss=%.0fMB\n", t, $1, $2/1024}'
    sleep 5
  done ) >"$work/server_res.log" &
mon=$!

set +e
"$work/cn35load" -base "http://127.0.0.1:$port" -out "$work/report.json" "$@" | tee "$work/load.txt"
rc=${PIPESTATUS[0]}
set -e

echo
echo "服务端资源（峰值，100% = 1 核）："
awk '{c=$2; r=$3; sub("cpu=","",c); sub("%","",c); sub("rss=","",r); sub("MB","",r);
      if (c+0>cm) cm=c+0; if (r+0>rm) rm=r+0; cs+=c; n++}
     END {if (n) printf "  CPU 峰值 %.0f%%  均值 %.0f%%  RSS 峰值 %d MB\n", cm, cs/n, rm}' "${work}/server_res.log"
if [[ -f "${state}/mercd35.sqlite3" ]]; then ls -lh "${state}"/mercd35.sqlite3* 2>/dev/null | awk '{print "  "$5" "$9}'; fi
echo "产物：${work} （report.json / load.txt / server.log / server_res.log）"
exit "$rc"

