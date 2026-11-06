#!/usr/bin/env python3
"""用户本地：校验原APK，解码到唯一源码目录，只修改Manifest和服务入口。"""
import argparse
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess
import os
import xml.etree.ElementTree as ET
from standalone_manifest import configure

ROOT = Path(__file__).resolve().parent.parent
ANDROID = "{http://schemas.android.com/apk/res/android}"


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--mode", choices=("server", "standalone"), default="server")
    ap.add_argument("--server-host", default="127.0.0.1:18091")
    args = ap.parse_args()
    if not re.fullmatch(r"[A-Za-z0-9_.-]+:[0-9]{1,5}", args.server_host):
        raise SystemExit("server-host 必须为 host:port")
    port = int(args.server_host.rsplit(":", 1)[1])
    if not 1 <= port <= 65535:
        raise SystemExit("端口不合法")
    if args.mode == "standalone" and args.server_host != "127.0.0.1:18091":
        raise SystemExit("单机引导器使用固定回环端口 18091")
    baseline = json.loads((ROOT / "client/baseline.json").read_text())
    original = ROOT / baseline["originalApk"]
    expected = baseline["originalApkSha256"]
    h = hashlib.sha256()
    with original.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(chunk)
    if h.hexdigest() != expected:
        raise SystemExit("原APK SHA-256 不匹配")
    source = ROOT / "client/decoded"
    if source.exists() or source.is_symlink():
        raise SystemExit("client/decoded 已存在；拒绝覆盖已有源码")
    jar = os.environ.get("APKTOOL_JAR")
    tool = ["java", "-jar", jar] if jar else [shutil.which("apktool") or "apktool"]
    version = subprocess.check_output(tool+["--version"], text=True).strip()
    if version != baseline["apktoolVersion"]:
        raise SystemExit("apktool 版本不匹配")
    subprocess.run(tool+["d", str(original), "-o", str(source)], check=True)
    manifest = source / "AndroidManifest.xml"
    tree = ET.parse(manifest)
    root = tree.getroot()
    if root.get("package") != "com.happyelements.miraclecn.uc":
        raise SystemExit("原包Manifest包名不符合基线；保留解码目录供检查")
    root.set("package", baseline["applicationId"])
    app = root.find("application")
    if app is None:
        raise SystemExit("Manifest缺少application")
    app.set(ANDROID+"debuggable", "true")
    if args.mode == "standalone":
        ET.SubElement(app, "provider", {
            ANDROID+"name":"com.mercd.backend.BootProvider",
            ANDROID+"authorities":baseline["applicationId"]+".mercdboot",
            ANDROID+"exported":"false"})
    if args.mode == "standalone":
        configure(root)
    ET.register_namespace("android", "http://schemas.android.com/apk/res/android")
    tree.write(manifest, encoding="utf-8", xml_declaration=True)
    gsp = source / "assets/gsp.txt"
    # 二进制读写：Windows 的文本模式会把 "\n" 翻译成 "\r\n"，使 gsp 末字段带上
    # 尾部 CR。客户端按 ";" 切分后末字段为 "true\r"，配置解析异常会表现为
    # 卡在“数据解压中”。原包该文件本无换行，此处统一按 LF 落盘。
    fields = gsp.read_bytes().decode("utf-8").strip().split(";")
    if len(fields) != 10:
        raise SystemExit("gsp字段形状已变化；停止，不修改DLL")
    fields[5] = args.server_host
    # 第8字段(0-based index 7)控制是否启用 UC SDK 登录：国服原版为 true，
    # 但在模拟器/测试机上 UC SDK 登录流程被阉割后无法完成，导致登录按钮点击
    # 后无任何请求发出。mac 可玩包为 false，走游客快速登录(heQuickLogin)。
    fields[7] = "false"
    gsp.write_bytes((";".join(fields)+"\n").encode("utf-8"))
    print("准备完成：client/decoded；只改Manifest与服务入口，未重建或签名APK")


if __name__ == "__main__":
    main()
