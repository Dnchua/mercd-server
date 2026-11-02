#!/usr/bin/env bash
# mercd35 CN 3.5.0 快速启动：构建 + 前台运行。
#
#   server/run.sh                        默认参数启动（127.0.0.1:18091，配 adb reverse）
#   server/run.sh --addr 0.0.0.0:18091 --server-host <本机IP>:18091   LAN 直连模式
#   server/run.sh --kill                 端口被旧 mercd35 占用时先终止再启动
#   server/run.sh --no-build -v          跳过构建、开请求摘要日志
#   server/run.sh -- <mercd35 原生参数>  -- 之后原样透传给 mercd35
#
# 环境变量覆盖：MERCD_ADDR / MERCD_SERVER_HOST / MERCD_ASSETS / MERCD_STATE /
# MERCD_BIN（默认值见下方）。停止：Ctrl-C。
# mercd35 提供只读运维端点；公开候选不含运营后台。
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"

# 已验收的 USB/回环配置：APK 内 gsp.txt 指向 127.0.0.1:18091，经
# `adb reverse tcp:18091 tcp:18091` 转发到本机。LAN 直连需同时覆盖
# --addr 与 --server-host（server-host 回环仅在监听回环时合法）。
addr="${MERCD_ADDR:-127.0.0.1:18091}"
server_host="${MERCD_SERVER_HOST:-127.0.0.1:18091}"
assets="${MERCD_ASSETS:-$repo_root/server/assets/cn350}"
state="${MERCD_STATE:-$repo_root/server/server-state-cn35}"
bin="${MERCD_BIN:-$repo_root/output/rebuilt/mercd35}"
build=true
auto_kill=false
verbose=false
extra_args=()

fail() {
  printf 'run.sh: %s\n' "$1" >&2
  exit 1
}

usage() {
  sed -n '2,11p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
  exit "${1:-0}"
}

while (($#)); do
  case "$1" in
    --addr)        addr="$2"; shift 2 ;;
    --server-host) server_host="$2"; shift 2 ;;
    --assets)      assets="$2"; shift 2 ;;
    --state)       state="$2"; shift 2 ;;
    --no-build)    build=false; shift ;;
    --kill)        auto_kill=true; shift ;;
    -v|--verbose)  verbose=true; shift ;;
    -h|--help)     usage 0 ;;
    --)            shift; extra_args+=("$@"); break ;;
    *)             usage 2 ;;
  esac
done

port="${addr##*:}"
[[ "$port" =~ ^[0-9]+$ ]] || fail "--addr 需要 host:port 形态，得到 $addr"

bin_name="$(basename "$bin")"
if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
  if ! $auto_kill; then
    fail "端口 $port 已被占用：
$(lsof -nP -iTCP:"$port" -sTCP:LISTEN)
  用 --addr 换端口（如 --addr 127.0.0.1:18099），或加 --kill 自动终止旧 ${bin_name}。"
  fi
  pids="$(lsof -tiTCP:"$port" -sTCP:LISTEN || true)"
  if [[ -n "$pids" ]]; then
    for pid in $pids; do
      cmd="$(ps -p "$pid" -o command= 2>/dev/null || true)"
      [[ "$cmd" == *"$bin_name"* ]] ||
        fail "端口 $port 被非 ${bin_name} 进程占用（PID $pid: ${cmd}），--kill 只自动清理本服务，请手动处理"
    done
    printf 'run.sh: --kill 终止旧 %s（PID: %s）\n' "$bin_name" "$(echo $pids | tr '\n' ' ')"
    kill $pids 2>/dev/null || true
    for _ in {1..20}; do
      lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1 || break
      sleep 0.25
    done
    if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
      printf 'run.sh: SIGTERM 未生效，改发 SIGKILL\n' >&2
      kill -9 $pids 2>/dev/null || true
      sleep 0.5
    fi
  fi
  ! lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1 ||
    fail "端口 $port 仍被占用（PID: $(lsof -tiTCP:"$port" -sTCP:LISTEN | tr '\n' ' ')），自动清理失败"
fi

test -d "$assets" || fail "assets 目录不存在: $assets"
mkdir -p "$state"

if $build; then
  printf 'run.sh: 构建 %s -> %s\n' "$bin_name" "$bin"
  (cd "$repo_root/server" && go build -trimpath -o "$bin" "./cmd/$bin_name")
fi
test -x "$bin" || fail "二进制不存在或不可执行: ${bin}（去掉 --no-build 先构建）"

# addr 是 0.0.0.0/:: 时浏览器访问用 127.0.0.1。
display_addr="$addr"
case "$addr" in
  0.0.0.0:*|:*|\[::\]:*) display_addr="127.0.0.1:$port" ;;
esac

args=(
  -addr "$addr"
  -server-host "$server_host"
  -assets "$assets"
  -state "$state"
  -reqlog "$state/requests.ndjson"
  -event-log "$state/events.ndjson"
)
$verbose && args+=(-v)
((${#extra_args[@]})) && args+=("${extra_args[@]}")

cat <<EOF
mercd35 CN 3.5.0 启动
  监听:      $addr
  回包地址:  $server_host
  运行库:    $state/mercd35.sqlite3
  assets:    $assets
  state:     $state
  路由表:    $bin --list-routes
  验证:      python3 "$repo_root/server/tools/verify_cn350_server.py" --base http://$display_addr --state-db "$state/mercd35.sqlite3"
停止: Ctrl-C
EOF

exec "$bin" "${args[@]}"
