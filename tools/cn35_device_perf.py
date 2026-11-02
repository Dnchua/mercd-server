#!/usr/bin/env python3
"""CN3.5 只读设备采样；不清 logcat、不重启、不点击、不改游戏数据。"""
import argparse
import concurrent.futures
import datetime as dt
import json
import pathlib
import re
import shlex
import statistics
import subprocess
import time
import urllib.request


def run(args, timeout=12):
    p = subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout)
    if p.returncode:
        raise RuntimeError(p.stderr.decode(errors="replace").strip() or f"command exit {p.returncode}")
    return p.stdout


def proc_stat(text):
    # comm 字段允许空格，不能直接按整行空格取固定列。
    rest = text[text.rfind(")") + 2:].split()
    return {"state": rest[0], "ticks": int(rest[11]) + int(rest[12]), "rss_pages": int(rest[21]),
            "start_ticks": int(rest[19])}


def summary(values):
    values = sorted(values)
    if not values:
        return {"n": 0}
    return {"n": len(values), "min": values[0], "median": statistics.median(values), "max": values[-1]}


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--device", required=True)
    p.add_argument("--package", default="com.happyelements.miraclecn.uc.cn350dbg")
    p.add_argument("--duration", type=int, default=30)
    p.add_argument("--interval", type=float, default=2)
    p.add_argument("--server-pid", type=int)
    p.add_argument("--base", default="http://127.0.0.1:18091")
    p.add_argument("--device-base", default="http://127.0.0.1:18091")
    p.add_argument("--label", default="manual")
    p.add_argument("--out", required=True)
    a = p.parse_args()
    if not 2 <= a.duration <= 300 or a.interval < 1:
        p.error("duration 必须 2..300 秒，interval >= 1 秒")
    out = pathlib.Path(a.out).resolve()
    out.mkdir(parents=True, exist_ok=True)
    adb = ["adb", "-s", a.device]

    def shell(cmd):
        return run(adb + ["shell", cmd]).decode(errors="replace")

    def screenshot(name):
        (out / name).write_bytes(run(adb + ["exec-out", "screencap", "-p"]))

    pid = shell("pidof " + shlex.quote(a.package)).strip().split()[0]
    hz = int(shell("getconf CLK_TCK"))
    page_size = int(shell("getconf PAGESIZE"))
    info = shell("getprop ro.product.cpu.abi; getprop ro.hardware; getprop ro.dalvik.vm.native.bridge; getprop ro.build.version.release")
    layers = shell("dumpsys SurfaceFlinger --list").splitlines()
    layer = next((x for x in layers if a.package in x and x.startswith("SurfaceView[") and "(BLAST)" in x), None)
    tids = shell(f"for f in /proc/{pid}/task/*/comm; do echo \"$f $(cat \"$f\")\"; done")
    # UnityMain 可有多个同名辅助线程，采样全部并用最高占用定位主线程。
    unity_tids = re.findall(r"/task/(\d+)/comm UnityMain$", tids, re.M)
    screenshot("before.png")
    (out / "memory-before.txt").write_text(shell("dumpsys meminfo " + shlex.quote(a.package)))
    (out / "gfxinfo.txt").write_text(shell("dumpsys gfxinfo " + shlex.quote(a.package) + " framestats"))
    samples, seen_frames, errors = [], set(), []
    previous = None
    started = time.monotonic()
    started_wall = dt.datetime.now().astimezone().isoformat()
    pool = concurrent.futures.ThreadPoolExecutor(max_workers=4)

    def device_cpu():
        cmd = f"cat /proc/uptime /proc/{pid}/stat"
        for tid in unity_tids:
            cmd += f" /proc/{pid}/task/{tid}/stat"
        return shell(cmd).strip().splitlines()

    def health():
        begin = time.monotonic()
        with urllib.request.urlopen(a.base.rstrip("/") + "/mercd/health", timeout=5) as r:
            if r.status != 200:
                raise RuntimeError(f"health HTTP {r.status}")
            r.read()
        return (time.monotonic() - begin) * 1000

    try:
        while time.monotonic() - started < a.duration:
            tick = time.monotonic()
            futures = {"proc": pool.submit(device_cpu), "health": pool.submit(health)}
            if layer:
                futures["frames"] = pool.submit(shell, "dumpsys SurfaceFlinger --latency " + shlex.quote(layer))
            if a.server_pid:
                futures["server"] = pool.submit(run, ["ps", "-p", str(a.server_pid), "-o", "%cpu=,rss=,time="])
            values = {}
            for key, f in futures.items():
                try:
                    values[key] = f.result()
                except Exception as e:
                    errors.append({"at_s": time.monotonic() - started, "source": key, "error": str(e)})
            row = {"at_s": time.monotonic() - started}
            if "proc" in values:
                lines = values["proc"]
                uptime = float(lines[0].split()[0])
                proc = proc_stat(lines[1])
                threads = {tid: proc_stat(line) for tid, line in zip(unity_tids, lines[2:])}
                row["rss_mib"] = proc["rss_pages"] * page_size / 1048576
                if previous and proc["start_ticks"] == previous[1]["start_ticks"]:
                    elapsed = uptime - previous[0]
                    if elapsed > 0:
                        row["app_cpu_one_core_pct"] = (proc["ticks"] - previous[1]["ticks"]) / hz / elapsed * 100
                        row["unity_cpu_one_core_pct"] = {tid: (v["ticks"] - previous[2][tid]["ticks"]) / hz / elapsed * 100
                                                         for tid, v in threads.items() if tid in previous[2]}
                previous = uptime, proc, threads
            if "health" in values:
                row["host_health_ms"] = values["health"]
            if "frames" in values:
                for line in values["frames"].splitlines()[1:]:
                    cols = line.split()
                    if len(cols) == 3 and all(x.isdigit() for x in cols):
                        actual = int(cols[1])
                        if 0 < actual < 9223372036854775807:
                            seen_frames.add(actual)
                # SurfaceFlinger 的环形缓存包含采样开始前的帧，用设备 uptime 排除。
                if "proc" in values:
                    row["presented_frames_in_sample"] = sum(int((uptime - a.interval) * 1e9) < t <= int(uptime * 1e9) for t in seen_frames)
            if "server" in values:
                cols = values["server"].decode().split()
                if len(cols) >= 3:
                    row.update(server_ps_cpu_pct=float(cols[0]), server_rss_mib=int(cols[1])/1024, server_cpu_time=cols[2])
            samples.append(row)
            print(json.dumps(row, ensure_ascii=False), flush=True)
            time.sleep(max(0, min(a.interval - (time.monotonic()-tick), a.duration-(time.monotonic()-started))))
    finally:
        pool.shutdown(wait=True)
    screenshot("after.png")
    (out / "memory-after.txt").write_text(shell("dumpsys meminfo " + shlex.quote(a.package)))
    # 仅保留性能时间线；URL 查询中的加密会话串不进入报告。
    log = shell("logcat -d -v threadtime -s Unity:I")
    relevant = r"Send Request|Request Done|Cost Time|Page Load Done|Time :|Exception|Error|can't be loaded|not found|not present|\.cs:| at "
    timeline = "\n".join(re.sub(r"(https?://[^\s?]+)\?[^\s]+", r"\1?redacted", line)
                         for line in log.splitlines() if re.search(relevant, line))
    (out / "unity-timeline.log").write_text(timeline + "\n")
    device_health = shell("curl -sS -o /dev/null --max-time 5 -w '%{http_code} %{time_starttransfer} %{time_total}' " +
                          shlex.quote(a.device_base.rstrip("/") + "/mercd/health")).strip()
    report = {"started": started_wall, "label": a.label, "device": a.device, "pid": pid,
              "device_info": info.splitlines(), "surface_layer": layer, "unity_tids": unity_tids,
              "duration_s": time.monotonic() - started, "device_health_http_ttfb_total_seconds": device_health,
              "app_cpu_one_core_pct": summary([x["app_cpu_one_core_pct"] for x in samples if "app_cpu_one_core_pct" in x]),
              "unity_busy_thread_cpu_pct": summary([max(x["unity_cpu_one_core_pct"].values(), default=0)
                                                     for x in samples if "unity_cpu_one_core_pct" in x]),
              "rss_mib": summary([x["rss_mib"] for x in samples if "rss_mib" in x]),
              "host_health_ms": summary([x["host_health_ms"] for x in samples if "host_health_ms" in x]),
              "notes": ["100% 表示一个核心；设备有多个核心时进程可超过 100%", "ps CPU 可能为历史平滑值；server_cpu_time 可用于相邻差分",
                        "gfxinfo 仅供参考，旧 Unity SurfaceView 不用它判定游戏帧率", "SurfaceFlinger 缓存最多约 128 帧，低帧率只是呈现现象，不能单独判定 GPU 瓶颈"],
              "samples": samples, "errors": errors}
    (out / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
    print(f"报告: {out / 'report.json'}", flush=True)


if __name__ == "__main__":
    main()
