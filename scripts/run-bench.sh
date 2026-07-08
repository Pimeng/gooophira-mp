#!/usr/bin/env bash
# scripts/run-bench.sh — 一键执行全部基准/压测套件
#
# 镜像 .github/workflows/bench.yml 的三个阶段（micro / load / tcp），
# 但默认参数 tuned for local runs（比 CI 更小更快）。
#
# 用法:
#   ./scripts/run-bench.sh                       # 跑全部三阶段，使用本地默认值
#   ./scripts/run-bench.sh --stage load           # 只跑 load (cmd/bench)
#   ./scripts/run-bench.sh --stage micro,tcp      # 跑 micro + tcp
#   ./scripts/run-bench.sh --quick                # 快速烟雾测试（更小参数）
#   ./scripts/run-bench.sh --keep-going           # 某阶段失败也继续跑后面的
#   ./scripts/run-bench.sh --summary-file out.md  # 把汇总写入文件
#
# 参数覆盖（环境变量）:
#   MICRO_COUNT=3 LOAD_CLIENTS=5000 TCP_DURATION=30s ./scripts/run-bench.sh
#
# 退出码:
#   0   全部阶段成功
#   1   参数错误
#   2+  至少一个阶段失败（仍会输出已完成阶段的汇总）

set -uo pipefail

# ─── 默认参数（本地友好，比 CI 默认值小） ───────────────────────────────────────
MICRO_COUNT="${MICRO_COUNT:-1}"
MICRO_PACKAGES="${MICRO_PACKAGES:-./internal/protocol/... ./internal/server/...}"

LOAD_CLIENTS="${LOAD_CLIENTS:-1000}"
LOAD_ROOMS="${LOAD_ROOMS:-100}"
LOAD_DURATION="${LOAD_DURATION:-15s}"
LOAD_SCENARIO="${LOAD_SCENARIO:-gameplay}"

TCP_CLIENTS="${TCP_CLIENTS:-50}"
TCP_ROOMS="${TCP_ROOMS:-5}"
TCP_DURATION="${TCP_DURATION:-20s}"
TCP_SCENARIO="${TCP_SCENARIO:-connection-storm}"

PROFILE="${PROFILE:-all}"
OUTPUT_DIR="${OUTPUT_DIR:-./tmp/profiles}"
KEEP_GOING=false
STAGE_FILTER=""
SUMMARY_FILE=""
QUICK=false

# ─── 颜色输出（非 TTY 时禁用） ─────────────────────────────────────────────────
if [[ -t 1 ]]; then
  C_RESET=$'\033[0m'; C_BOLD=$'\033[1m'; C_CYAN=$'\033[36m'
  C_GREEN=$'\033[32m'; C_YELLOW=$'\033[33m'; C_RED=$'\033[31m'; C_DIM=$'\033[2m'
else
  C_RESET=""; C_BOLD=""; C_CYAN=""; C_GREEN=""; C_YELLOW=""; C_RED=""; C_DIM=""
fi

usage() {
  cat <<'EOF'
usage: ./scripts/run-bench.sh [options]

options:
  --stage <list>        逗号分隔的阶段列表: micro,load,tcp,all (默认: all)
  --quick               快速烟雾测试（覆盖为更小参数）
  --keep-going         某阶段失败仍继续后续阶段
  --summary-file <path> 把汇总写入 markdown 文件
  --output-dir <path>   profile 输出根目录 (默认: ./tmp/profiles)
  --profile <type>      pprof 类型: cpu,mem,goroutine,mutex,block,all,none (默认: all)
  --no-profile          等同 --profile none
  -h, --help            显示帮助

load 阶段参数:
  --load-clients <n>    --load-rooms <n>    --load-duration <d>    --load-scenario <s>

tcp 阶段参数:
  --tcp-clients <n>     --tcp-rooms <n>     --tcp-duration <d>     --tcp-scenario <s>

micro 阶段参数:
  --micro-count <n>     每个基准运行次数 (默认: 1, CI 用 3)

环境变量覆盖: MICRO_COUNT / LOAD_CLIENTS / LOAD_ROOMS / LOAD_DURATION /
             LOAD_SCENARIO / TCP_CLIENTS / TCP_ROOMS / TCP_DURATION /
             TCP_SCENARIO / PROFILE / OUTPUT_DIR
EOF
}

# ─── 参数解析 ─────────────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --stage)         STAGE_FILTER="$2"; shift 2 ;;
    --quick)         QUICK=true; shift ;;
    --keep-going)    KEEP_GOING=true; shift ;;
    --summary-file)  SUMMARY_FILE="$2"; shift 2 ;;
    --output-dir)   OUTPUT_DIR="$2"; shift 2 ;;
    --profile)       PROFILE="$2"; shift 2 ;;
    --no-profile)    PROFILE="none"; shift ;;
    --micro-count)   MICRO_COUNT="$2"; shift 2 ;;
    --load-clients)  LOAD_CLIENTS="$2"; shift 2 ;;
    --load-rooms)    LOAD_ROOMS="$2"; shift 2 ;;
    --load-duration) LOAD_DURATION="$2"; shift 2 ;;
    --load-scenario) LOAD_SCENARIO="$2"; shift 2 ;;
    --tcp-clients)   TCP_CLIENTS="$2"; shift 2 ;;
    --tcp-rooms)     TCP_ROOMS="$2"; shift 2 ;;
    --tcp-duration)  TCP_DURATION="$2"; shift 2 ;;
    --tcp-scenario)  TCP_SCENARIO="$2"; shift 2 ;;
    -h|--help)       usage; exit 0 ;;
    *) echo "${C_RED}未知参数: $1${C_RESET}" >&2; usage >&2; exit 1 ;;
  esac
done

# --quick 覆盖为最小参数
if [[ "$QUICK" == true ]]; then
  MICRO_COUNT=1
  LOAD_CLIENTS=100; LOAD_ROOMS=5; LOAD_DURATION=5s
  TCP_CLIENTS=10;  TCP_ROOMS=0;  TCP_DURATION=5s
  PROFILE="${PROFILE:-none}"
  [[ "$PROFILE" == "all" ]] && PROFILE="none"
fi

# stage filter 解析
[[ -z "$STAGE_FILTER" || "$STAGE_FILTER" == "all" ]] && STAGE_FILTER="micro,load,tcp"
IFS=',' read -ra STAGES <<< "$STAGE_FILTER"
for s in "${STAGES[@]}"; do
  case "$s" in
    micro|load|tcp) ;;
    *) echo "${C_RED}未知 stage: $s${C_RESET}" >&2; exit 1 ;;
  esac
done

# profile 转 bench 工具的 -profile 参数（none → 空字符串）
profile_arg() {
  case "$1" in
    none|""|no) echo "" ;;
    *) echo "$1" ;;
  esac
}

# ─── 前置检查 ─────────────────────────────────────────────────────────────────
command -v go >/dev/null 2>&1 || { echo "${C_RED}错误: 未找到 go 命令${C_RESET}" >&2; exit 1; }

HAVE_JQ=false
if command -v jq >/dev/null 2>&1; then
  HAVE_JQ=true
else
  echo "${C_YELLOW}提示: 未安装 jq，将跳过 load/tcp 的表格汇总（仅输出 JSON 原始文件路径）${C_RESET}" >&2
fi

# ─── 跟踪状态 ─────────────────────────────────────────────────────────────────
FAILED_STAGES=()
MICRO_DIR="$OUTPUT_DIR/micro"
LOAD_DIR="$OUTPUT_DIR/load"
TCP_DIR="$OUTPUT_DIR/tcpconnect"
# 每阶段生成的 summary.md 路径，按完成顺序累积，用于最后合并
STAGE_SUMMARY_FILES=()

# ─── 阶段: micro benchmarks ────────────────────────────────────────────────────
run_micro() {
  echo "${C_BOLD}${C_CYAN}═══ [1] Micro Benchmarks ═══${C_RESET}"
  mkdir -p "$MICRO_DIR"
  : > "$MICRO_DIR/bench.txt"  # 清空上次残留

  # go test 的 -cpuprofile/-memprofile 不支持多包同时测试，按包分别运行。
  # 每个包生成独立的 cpu_<tag>.out / mem_<tag>.out。
  local -a pkgs
  read -ra pkgs <<< "$MICRO_PACKAGES"

  for pkg in "${pkgs[@]}"; do
    # 从包路径生成 tag: ./internal/protocol/... → protocol
    local tag="${pkg%/...}"   # 去掉 /... 后缀
    tag="${tag%/}"            # 去掉尾 /
    tag="${tag##*/}"          # 取最后一段
    [[ -z "$tag" || "$tag" == "." ]] && tag="pkg"

    local cmd="go test -bench=Benchmark -benchmem -count=$MICRO_COUNT \
      -cpuprofile=$MICRO_DIR/cpu_${tag}.out -memprofile=$MICRO_DIR/mem_${tag}.out \
      -run='^$' $pkg"

    echo "${C_DIM}$cmd${C_RESET}"
    if ! bash -c "$cmd" 2>&1 | tee -a "$MICRO_DIR/bench.txt"; then
      echo "${C_RED}micro benchmarks 失败 ($pkg)${C_RESET}" >&2
      FAILED_STAGES+=("micro")
      return 1
    fi
  done

  # lock profiles（仅 server 包，count=1 避免 mutex/block 采样相互干扰）
  local lock_cmd="go test -bench=Benchmark -benchmem -count=1 \
    -mutexprofile=$MICRO_DIR/mutex.out -blockprofile=$MICRO_DIR/block.out \
    -run='^$' ./internal/server/"
  echo "${C_DIM}$lock_cmd${C_RESET}"
  if ! bash -c "$lock_cmd" 2>&1 | tee -a "$MICRO_DIR/bench.txt"; then
    echo "${C_YELLOW}micro lock profiles 失败（继续）${C_RESET}" >&2
  fi

  # 汇总表：bench.txt 中 Benchmark 行格式: BenchmarkXxx-N <ns/op> ns/op <B/op> B/op <allocs> allocs/op
  # awk 提取 $1=BenchmarkXxx-N, $3+$4=ns/op, $5+$6=B/op, $7+$8=allocs/op
  {
    echo "## Micro Benchmarks"
    echo ""
    echo "| Benchmark | ns/op | B/op | allocs/op |"
    echo "|---|---:|---:|---:|"
    awk '/^Benchmark/ {
      printf "| %s | %s %s | %s %s | %s %s |\n", $1, $3, $4, $5, $6, $7, $8
    }' "$MICRO_DIR/bench.txt" | sort -u
    echo ""
  } > "$MICRO_DIR/summary.md"
  cat "$MICRO_DIR/summary.md"
  STAGE_SUMMARY_FILES+=("$MICRO_DIR/summary.md")
}

# ─── 阶段: load test (cmd/bench) ──────────────────────────────────────────────
run_load() {
  echo "${C_BOLD}${C_CYAN}═══ [2] Load Test (cmd/bench) ═══${C_RESET}"
  mkdir -p "$LOAD_DIR"

  local prof_arg
  prof_arg="$(profile_arg "$PROFILE")"

  local cmd="go run ./cmd/bench/ \
    -clients=$LOAD_CLIENTS -rooms=$LOAD_ROOMS \
    -duration=$LOAD_DURATION -scenario=$LOAD_SCENARIO \
    -profile-dir=$LOAD_DIR -json"
  [[ -n "$prof_arg" ]] && cmd+=" -profile=$prof_arg"

  echo "${C_DIM}$cmd${C_RESET}"
  if ! bash -c "$cmd" > "$LOAD_DIR/result.json" 2> "$LOAD_DIR/stderr.log"; then
    echo "${C_RED}load test 失败${C_RESET}" >&2
    cat "$LOAD_DIR/stderr.log" >&2
    FAILED_STAGES+=("load")
    return 1
  fi

  if [[ "$HAVE_JQ" == true && -s "$LOAD_DIR/result.json" ]]; then
    jq -r '
      if (.results | length) > 0 then
        .results[0] as $r |
        (($r.cmd_latency.count // 0) > 0) as $has_lat |
        ($r.duration // 0) as $dur_ns |
        "## Load Test",
        "",
        "| Metric | Value |",
        "|---|---|",
        "| Scenario | \($r.name) |",
        "| Clients | \($r.config.clients // "N/A") |",
        "| Rooms | \($r.config.rooms // "N/A") |",
        "| Duration | \(if $dur_ns > 0 then ($dur_ns / 1000000000) else 0 end)s |",
        "| Throughput | \((($r.throughput.avg_cmds_per_sec // 0) * 100 | floor) / 100) cmd/s |",
        "| Cycles/sec | \(if $dur_ns > 0 then ((($r.throughput.cycles_completed // 0) / ($dur_ns / 1000000000)) * 100 | floor) / 100 else 0 end) |",
        "| Latency avg/min/max | \(if $has_lat then "\(($r.cmd_latency.mean // 0)/1000 | floor)us / \(($r.cmd_latency.min // 0)/1000 | floor)us / \(($r.cmd_latency.max // 0)/1000 | floor)us" else "N/A" end) |",
        "| Latency p50/p90/p99 | \(if $has_lat then "\(($r.cmd_latency.p50 // 0)/1000 | floor)us / \(($r.cmd_latency.p90 // 0)/1000 | floor)us / \(($r.cmd_latency.p99 // 0)/1000 | floor)us" else "N/A" end) |",
        "| Peak goroutines | \($r.runtime.peak_goroutines // "N/A") |",
        "| Peak heap | \((($r.runtime.peak_heap_mb // 0) * 10 | floor) / 10) MB |",
        "| GC count | \($r.runtime.num_gc // "N/A") |",
        "| Errors | \($r.errors.total // 0) |",
        ""
      else
        "## Load Test",
        "",
        "_(no results — bench failed to produce output)_",
        ""
      end
    ' "$LOAD_DIR/result.json" > "$LOAD_DIR/summary.md"
    cat "$LOAD_DIR/summary.md"
  else
    echo "## Load Test"
    echo ""
    echo "raw JSON: \`$LOAD_DIR/result.json\`"
    echo ""
    { echo "## Load Test"; echo ""; echo "raw JSON: \`$LOAD_DIR/result.json\`"; echo ""; } > "$LOAD_DIR/summary.md"
  fi
  STAGE_SUMMARY_FILES+=("$LOAD_DIR/summary.md")
}

# ─── 阶段: tcp connect load test (cmd/tcpconnectbench) ────────────────────────
run_tcp() {
  echo "${C_BOLD}${C_CYAN}═══ [3] TCP Connect Load Test (cmd/tcpconnectbench) ═══${C_RESET}"
  mkdir -p "$TCP_DIR"

  local prof_arg
  prof_arg="$(profile_arg "$PROFILE")"

  local cmd="go run ./cmd/tcpconnectbench/ \
    -clients=$TCP_CLIENTS -rooms=$TCP_ROOMS \
    -duration=$TCP_DURATION -scenario=$TCP_SCENARIO \
    -profile-dir=$TCP_DIR -json"
  [[ -n "$prof_arg" ]] && cmd+=" -profile=$prof_arg"

  echo "${C_DIM}$cmd${C_RESET}"
  if ! bash -c "$cmd" > "$TCP_DIR/result.json" 2> "$TCP_DIR/stderr.log"; then
    echo "${C_RED}tcp connect load test 失败${C_RESET}" >&2
    cat "$TCP_DIR/stderr.log" >&2
    FAILED_STAGES+=("tcp")
    return 1
  fi

  if [[ "$HAVE_JQ" == true && -s "$TCP_DIR/result.json" ]]; then
    jq -r '
      if (.results | length) > 0 then
        .results[0] as $r |
        (($r.cmd_latency.count // 0) > 0) as $has_lat |
        ($r.duration // 0) as $dur_ns |
        "## TCP Connect Load Test",
        "",
        "| Metric | Value |",
        "|---|---|",
        "| Scenario | \($r.name) |",
        "| Clients | \($r.config.clients // "N/A") |",
        "| Rooms | \($r.config.rooms // "N/A") |",
        "| Duration | \(if $dur_ns > 0 then ($dur_ns / 1000000000) else 0 end)s |",
        "| Throughput | \((($r.throughput.avg_cmds_per_sec // 0) * 100 | floor) / 100) cmd/s |",
        "| TCP connect avg/min/max | \(($r.connect_latency.mean // 0)/1000 | floor)us / \(($r.connect_latency.min // 0)/1000 | floor)us / \(($r.connect_latency.max // 0)/1000 | floor)us |",
        "| TCP connect p50/p90/p99 | \(($r.connect_latency.p50 // 0)/1000 | floor)us / \(($r.connect_latency.p90 // 0)/1000 | floor)us / \(($r.connect_latency.p99 // 0)/1000 | floor)us |",
        "| Auth RTT avg/min/max | \(($r.auth_latency.mean // 0)/1000 | floor)us / \(($r.auth_latency.min // 0)/1000 | floor)us / \(($r.auth_latency.max // 0)/1000 | floor)us |",
        "| Auth RTT p50/p90/p99 | \(($r.auth_latency.p50 // 0)/1000 | floor)us / \(($r.auth_latency.p90 // 0)/1000 | floor)us / \(($r.auth_latency.p99 // 0)/1000 | floor)us |",
        "| Cmd latency avg/min/max | \(if $has_lat then "\(($r.cmd_latency.mean // 0)/1000 | floor)us / \(($r.cmd_latency.min // 0)/1000 | floor)us / \(($r.cmd_latency.max // 0)/1000 | floor)us" else "N/A" end) |",
        "| Cmd latency p50/p90/p99 | \(if $has_lat then "\(($r.cmd_latency.p50 // 0)/1000 | floor)us / \(($r.cmd_latency.p90 // 0)/1000 | floor)us / \(($r.cmd_latency.p99 // 0)/1000 | floor)us" else "N/A" end) |",
        "| Peak goroutines | \($r.runtime.peak_goroutines // "N/A") |",
        "| Peak heap | \((($r.runtime.peak_heap_mb // 0) * 10 | floor) / 10) MB |",
        "| GC count | \($r.runtime.num_gc // "N/A") |",
        "| Errors | \($r.errors.total // 0) |",
        ""
      else
        "## TCP Connect Load Test",
        "",
        "_(no results — bench failed to produce output)_",
        ""
      end
    ' "$TCP_DIR/result.json" > "$TCP_DIR/summary.md"
    cat "$TCP_DIR/summary.md"
  else
    echo "## TCP Connect Load Test"
    echo ""
    echo "raw JSON: \`$TCP_DIR/result.json\`"
    echo ""
    { echo "## TCP Connect Load Test"; echo ""; echo "raw JSON: \`$TCP_DIR/result.json\`"; echo ""; } > "$TCP_DIR/summary.md"
  fi
  STAGE_SUMMARY_FILES+=("$TCP_DIR/summary.md")
}

# ─── 主流程 ───────────────────────────────────────────────────────────────────
echo "${C_BOLD}Phira MP 一键基准测试${C_RESET}"
echo "${C_DIM}stage=$STAGE_FILTER  profile=$PROFILE  output=$OUTPUT_DIR${C_RESET}"
if [[ "$QUICK" == true ]]; then
  echo "${C_YELLOW}[quick 模式] 参数已缩小，仅用于烟雾测试${C_RESET}"
fi
echo ""

START_TS=$(date +%s)

for stage in "${STAGES[@]}"; do
  case "$stage" in
    micro) run_micro  || [[ "$KEEP_GOING" == true ]] || break ;;
    load)  run_load   || [[ "$KEEP_GOING" == true ]] || break ;;
    tcp)   run_tcp    || [[ "$KEEP_GOING" == true ]] || break ;;
  esac
  echo ""
done

ELAPSED=$(( $(date +%s) - START_TS ))

# ─── 产物索引 + pprof 提示 + 总览（写入 footer.md 一并累积） ─────────────────
FOOTER_FILE="$OUTPUT_DIR/footer.md"
{
  echo "## Artifacts"
  echo ""
  echo "| 类型 | 路径 |"
  echo "|---|---|"
  [[ -f "$MICRO_DIR/bench.txt" ]]  && echo "| micro 原始输出 | \`$MICRO_DIR/bench.txt\` |"
  # micro 阶段按包分别生成 cpu_<tag>.out / mem_<tag>.out，用 glob 枚举
  for _f in "$MICRO_DIR"/cpu_*.out "$MICRO_DIR"/mem_*.out; do
    [[ -f "$_f" ]] && echo "| micro prof | \`$_f\` |"
  done
  [[ -f "$MICRO_DIR/mutex.out" ]]  && echo "| micro mutex prof | \`$MICRO_DIR/mutex.out\` |"
  [[ -f "$MICRO_DIR/block.out" ]]  && echo "| micro block prof | \`$MICRO_DIR/block.out\` |"
  [[ -f "$LOAD_DIR/result.json" ]] && echo "| load JSON | \`$LOAD_DIR/result.json\` |"
  if [[ "$PROFILE" != "none" && "$PROFILE" != "" ]]; then
    [[ -f "$LOAD_DIR/cpu.pprof" ]]         && echo "| load CPU pprof | \`$LOAD_DIR/cpu.pprof\` |"
    [[ -f "$LOAD_DIR/heap.pprof" ]]         && echo "| load heap pprof | \`$LOAD_DIR/heap.pprof\` |"
    [[ -f "$LOAD_DIR/goroutine.pprof" ]]   && echo "| load goroutine pprof | \`$LOAD_DIR/goroutine.pprof\` |"
    [[ -f "$LOAD_DIR/mutex.pprof" ]]        && echo "| load mutex pprof | \`$LOAD_DIR/mutex.pprof\` |"
    [[ -f "$LOAD_DIR/block.pprof" ]]        && echo "| load block pprof | \`$LOAD_DIR/block.pprof\` |"
  fi
  [[ -f "$TCP_DIR/result.json" ]]  && echo "| tcp JSON | \`$TCP_DIR/result.json\` |"
  if [[ "$PROFILE" != "none" && "$PROFILE" != "" ]]; then
    [[ -f "$TCP_DIR/cpu.pprof" ]]           && echo "| tcp CPU pprof | \`$TCP_DIR/cpu.pprof\` |"
    [[ -f "$TCP_DIR/heap.pprof" ]]          && echo "| tcp heap pprof | \`$TCP_DIR/heap.pprof\` |"
    [[ -f "$TCP_DIR/goroutine.pprof" ]]     && echo "| tcp goroutine pprof | \`$TCP_DIR/goroutine.pprof\` |"
    [[ -f "$TCP_DIR/mutex.pprof" ]]         && echo "| tcp mutex pprof | \`$TCP_DIR/mutex.pprof\` |"
    [[ -f "$TCP_DIR/block.pprof" ]]         && echo "| tcp block pprof | \`$TCP_DIR/block.pprof\` |"
  fi
  echo ""

  if [[ "$PROFILE" != "none" && "$PROFILE" != "" ]]; then
    echo "## pprof 分析命令"
    echo ""
    echo '```bash'
    echo "# load"
    echo "go tool pprof -http=:8080 $LOAD_DIR/cpu.pprof"
    echo "go tool pprof -http=:8080 $LOAD_DIR/heap.pprof"
    echo "# tcp"
    echo "go tool pprof -http=:8080 $TCP_DIR/cpu.pprof"
    echo '```'
    echo ""
  fi

  footer_status="✅"
  [[ ${#FAILED_STAGES[@]} -gt 0 ]] && footer_status="⚠️"
  echo "## Summary"
  echo ""
  echo "| 阶段 | 状态 | 耗时 |"
  echo "|---|---|---|"
  echo "| total | $footer_status | ${ELAPSED}s |"
  echo ""
} > "$FOOTER_FILE"
cat "$FOOTER_FILE"
STAGE_SUMMARY_FILES+=("$FOOTER_FILE")

# ─── 写入 summary 文件 ────────────────────────────────────────────────────────
if [[ -n "$SUMMARY_FILE" ]]; then
  {
    echo "# Phira MP 基准测试报告"
    echo ""
    echo "生成时间: $(date '+%Y-%m-%d %H:%M:%S')"
    echo "stage=$STAGE_FILTER  profile=$PROFILE  elapsed=${ELAPSED}s"
    echo ""
    for f in "${STAGE_SUMMARY_FILES[@]}"; do
      [[ -f "$f" ]] && cat "$f"
    done
  } > "$SUMMARY_FILE"
  echo "${C_GREEN}汇总已写入: $SUMMARY_FILE${C_RESET}"
fi

# ─── 退出 ──────────────────────────────────────────────────────────────────────
if [[ ${#FAILED_STAGES[@]} -gt 0 ]]; then
  echo "${C_RED}失败阶段: ${FAILED_STAGES[*]}${C_RESET}" >&2
  exit 2
fi
echo "${C_GREEN}全部阶段成功 (${ELAPSED}s)${C_RESET}"
exit 0
