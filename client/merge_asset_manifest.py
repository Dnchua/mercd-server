#!/usr/bin/env python3
"""为单机资源生成 5 字段服务端清单（直接走 APK 内 StreamingAssets，不追加第 6 字段 MD5）。"""
import argparse
import json
from pathlib import Path
import time

SUFFIX = ".Android.unity3d"
ALIASES = {"common/bg-map": "common/bg-map_1136",
           "common/bg_message": "common/bg_message_fs",
           "common/bg_shout": "common/bg_shout_fs"}


def generate(root, version=None):
    types = json.loads((Path(__file__).parent / "asset-types.json").read_text())
    manifest = root / "assets/backend/cn350/serve_asset_info_android4_v2_short.txt"
    metadata, assets = [], {}
    for row in manifest.read_text().strip().split("@"):
        fields = row.split(",")
        if fields[0] in {"version", "ios_version", "android_version", "maintenance"}:
            metadata.append(fields)
            continue
        if len(fields) < 5 or not 0 <= int(fields[1]) < len(types):
            raise ValueError("基线清单形状不符合约定")
        name = types[int(fields[1])][1] + fields[0]
        assets[name] = fields[:5]
    unity = root / "assets/unity"
    bundles = {}
    for path in sorted(unity.rglob("*")):
        if path.is_symlink():
            raise ValueError("不允许资源符号链接：" + str(path))
        if not path.is_file() or not path.name.endswith(SUFFIX) or path.name.startswith("._"):
            continue
        name = path.relative_to(unity).as_posix()[:-len(SUFFIX)]
        with path.open("rb") as stream:
            if stream.read(9) not in (b"UnityWeb\0", b"UnityRaw\0"):
                raise ValueError("非法 Unity bundle：" + name)
        bundles[name] = {"file": name + SUFFIX, "size": path.stat().st_size}
        if name not in assets:
            matches = [(i, prefix) for i, (_, prefix, _) in enumerate(types) if name.startswith(prefix)]
            if not matches:
                raise ValueError("未知资源类型：" + name)
            idx, prefix = max(matches, key=lambda item: (len(item[1]), item[0]))
            assets[name] = [name[len(prefix):], str(idx), "1", "0", "1"]
    if not bundles:
        raise ValueError("缺少 assets/unity 资源")
    for name in assets:
        if ALIASES.get(name, name) not in bundles:
            raise ValueError("清单资源缺失：" + name)
    for fields in metadata:
        if fields[0] == "version":
            fields[1] = str(max(int(fields[1]) + 1, version or time.time_ns() // 1000000))
    manifest.write_bytes(("@".join(",".join(f) for f in metadata + [assets[n] for n in sorted(assets)]) + "\n").encode())
    legacy_index = root / "assets/backend/local-assets.json"
    if legacy_index.exists():
        legacy_index.unlink()
    apktool_yml = root / "apktool.yml"
    if apktool_yml.is_file():
        yml_text = apktool_yml.read_text()
        if "\n- unity3d\n" not in yml_text and "doNotCompress:\n" in yml_text:
            apktool_yml.write_text(yml_text.replace("doNotCompress:\n", "doNotCompress:\n- unity3d\n", 1))
    print(f"单机本地清单：{len(assets)} 项，{len(bundles)} 个文件；5 字段无 MD5 模式（直读 APK 内 StreamingAssets）")
    return {"assets": len(assets), "bundles": list(bundles.values())}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("root", type=Path)
    args = parser.parse_args()
    generate(args.root.resolve())
