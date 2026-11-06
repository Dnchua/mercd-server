#!/usr/bin/env python3
"""单机启动页阻止 Unity 在资源预置完成前初始化缓存。"""
from pathlib import Path
import sys
import xml.etree.ElementTree as ET

ANDROID = "{http://schemas.android.com/apk/res/android}"
BOOT = "com.mercd.backend.BootActivity"


def configure(root):
    app = root.find("application")
    if app is None:
        raise ValueError("Manifest 缺少 application")
    if app.find("activity[@" + ANDROID + "name='" + BOOT + "']") is not None:
        return
    launchers = []
    for activity in app.findall("activity"):
        for intent in activity.findall("intent-filter"):
            if any(c.get(ANDROID + "name") == "android.intent.category.LAUNCHER" for c in intent.findall("category")):
                launchers.append((activity, intent))
    if len(launchers) != 1:
        raise ValueError("单机模式要求唯一启动 Activity")
    original, intent = launchers[0]
    target = original.get(ANDROID + "name")
    if target.startswith("."):
        target = root.get("package") + target
    elif "." not in target:
        target = root.get("package") + "." + target
    gate = ET.SubElement(app, "activity", {
        ANDROID + "name": BOOT, ANDROID + "exported": "true",
        ANDROID + "screenOrientation": "landscape",
        ANDROID + "theme": "@android:style/Theme.Black.NoTitleBar.Fullscreen"})
    original.remove(intent)
    gate.append(intent)
    ET.SubElement(gate, "meta-data", {ANDROID + "name": "mercd.gameActivity", ANDROID + "value": target})


if __name__ == "__main__":
    path = Path(sys.argv[1])
    tree = ET.parse(path)
    if not any(p.get(ANDROID + "name") == "com.mercd.backend.BootProvider" for p in tree.getroot().findall("application/provider")):
        raise SystemExit("decoded 不是 standalone 模式，请先使用 prepare.py --mode standalone")
    configure(tree.getroot())
    ET.register_namespace("android", ANDROID[1:-1])
    tree.write(path, encoding="utf-8", xml_declaration=True)
