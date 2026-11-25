#!/usr/bin/env python3
"""按 event_banner_map.json 生成历史/当期活动横幅 bundle。

客户端 StageSelectListParts Tab.PastEvent 按 EventStageListRow 用
mainAsset 贴图 "event/event_<id>" 渲染活动行。本工具把映射的源 bundle
复制为 inputs/files/event/event_<id>.Android.unity3d（inputs/ 已 gitignore，
Unity 资源不入库），并校验文件头为 UnityWeb/UnityRaw。

用法：python3 tools/build_event_banners.py
"""

from __future__ import annotations

import json
import shutil
import sys
from pathlib import Path

SERVER = Path(__file__).resolve().parents[1]
REPO = SERVER.parent
MAP_PATH = SERVER / "assets" / "cn350" / "event_banner_map.json"
FILES_DIR = REPO / "inputs" / "files"
OUT_DIR = FILES_DIR / "event"


def main() -> int:
    doc = json.loads(MAP_PATH.read_text(encoding="utf-8"))
    OUT_DIR.mkdir(parents=True, exist_ok=True)
    made = 0
    for eid, spec in sorted(doc["map"].items(), key=lambda kv: int(kv[0])):
        src = FILES_DIR / f"{spec['source']}.Android.unity3d"
        if not src.is_file():
            print(f"FAIL: event {eid} 源缺失 {src}", file=sys.stderr)
            return 1
        head = src.open("rb").read(9)
        if head not in (b"UnityWeb\0", b"UnityRaw\0"):
            print(f"FAIL: event {eid} 源魔数非法 {head!r}", file=sys.stderr)
            return 1
        dst = OUT_DIR / f"event_{eid}.Android.unity3d"
        shutil.copyfile(src, dst)
        made += 1
        print(f"{eid} <- {spec['source']} ({spec['level']})")
    print(f"OK: {made} event bundles -> {OUT_DIR}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
