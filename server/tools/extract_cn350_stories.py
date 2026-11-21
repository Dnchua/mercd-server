#!/usr/bin/env python3
"""Extract, re-index, and normalize CN3.5 main story scripts from a Story APK.

Reads the player-packaged story APK read-only, selects the 533 official CN3.5
main-story episodes (Chapters 1..11) defined in cn350_main_story_catalog.json,
restores original episode IDs / chapter groupings / Chinese titles, repairs
SceneDocument speaker / ActressDocument active / display_name / type=navi fields,
validates 100% asset closure against CN3.5 asset_info + overlay bundles, and
writes compacted JSON files into gitignored server/assets/cn350/stories/.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import zipfile
from pathlib import Path

ASSET_TYPE_PREFIXES = [
    "area/area_",
    "background/background",
    "common/",
    "common_icon/",
    "common_text/",
    "effect_pd/",
    "effect_ss/",
    "equipment/equipment_",
    "event/event_",
    "monster/monster_",
    "monster/monster_btn_",
    "monster/monster_gray_btn_",
    "monster/monster_large_ns_",
    "monster/monster_list_",
    "monster/monster_rectangle_",
    "monster/monster_square_",
    "skill/skill_",
    "sound/",
    "storyactress/storyactress",
    "storyactress_hd/storyactress",
    "storybackground/storybackground",
    "swf/",
    "task/",
    "unit/unit_",
    "unit/unit_btn_",
    "unit/unit_btn_",
    "unit/unit_gray_btn_",
    "unit/unit_large_",
    "unit/unit_large_ns_",
    "unit/unit_list_",
    "unit/unit_rectangle_",
    "unit/unit_silhouette_",
    "unit/unit_square_",
    "unit_hd/unit_large_",
    "unit_hd/unit_large_ns_",
    "unit_hd/unit_square_",
    "stamp/stamp_",
]

KANA_RE = re.compile(r"[\u3040-\u30ff]")
TEXTASSET_RE = re.compile(r"^StoryYamlData_(\d+)$")
NAVI_IDS = {10002, 10004}


def load_accepted_assets(assets_dir: Path, extra_unity_dir: Path | None) -> tuple[set[str], set[str]]:
    base: set[str] = set()
    short_file = assets_dir / "serve_asset_info_android4_v2_short.txt"
    raw = short_file.read_text(encoding="utf-8")
    for row in raw.rstrip("\n").split("@"):
        parts = row.split(",")
        if len(parts) < 5:
            continue
        try:
            idx = int(parts[1])
        except ValueError:
            continue
        if 0 <= idx < len(ASSET_TYPE_PREFIXES):
            base.add(ASSET_TYPE_PREFIXES[idx] + parts[0])

    overlay = set(base)
    if extra_unity_dir and extra_unity_dir.is_dir():
        for root, _, files in os.walk(extra_unity_dir):
            for fn in files:
                if fn.endswith(".Android.unity3d"):
                    rel = os.path.relpath(os.path.join(root, fn), extra_unity_dir).replace(os.sep, "/")
                    overlay.add(rel[: -len(".Android.unity3d")])
    return base, overlay


def normalize_speaker(raw_name: str, speaker_map: dict[str, str]) -> str:
    if raw_name in speaker_map:
        return speaker_map[raw_name]
    if raw_name in ("<name>", "<nmae>"):
        return "<name>"
    if raw_name in ("", "‐", "-", "―"):
        return ""
    return raw_name


def default_xy() -> dict:
    return {
        "xy": {"x": 0, "y": 0},
        "face_xy": {"x": 0, "y": 0},
        "emotion_xy": {"x": 0, "y": 0},
    }


def normalize_story_doc(raw_doc: dict, ep: dict, speaker_map: dict[str, str]) -> tuple[dict, list[str], list[str]]:
    warnings: list[str] = []
    used_actors: set[int] = set()
    used_bgs: set[tuple[str, str]] = set()
    old_scenes = raw_doc.get("story", {}).get("scenes_map_array", [])
    new_scenes: list[dict] = []

    for i, sc in enumerate(old_scenes):
        if not isinstance(sc, dict):
            raise ValueError(f"story {ep['id']} scene[{i}] is not an object")
        text = sc.get("text", "")
        if not isinstance(text, str) or len(text) < 2:
            raise ValueError(f"story {ep['id']} scene[{i}] text invalid: {text!r}")

        raw_name = sc.get("name")
        if raw_name is None:
            raw_name = sc.get("speaker", "")
        speaker = normalize_speaker(str(raw_name), speaker_map)
        if KANA_RE.search(speaker):
            warnings.append(f"story {ep['id']} scene[{i}] residual kana speaker: {raw_name!r} -> {speaker!r}")

        new_sc: dict = {
            "speaker": speaker,
            "text": text,
            "background": str(sc.get("background", "")),
            "timezone": str(sc.get("timezone", "")),
            "effect": str(sc.get("effect", "")),
            "text_size": str(sc.get("text_size", "")),
            "window_type": str(sc.get("window_type", "")),
            "bgm": str(sc.get("bgm", "")),
            "effect_parameter": str(sc.get("effect_parameter", "")),
        }
        if new_sc["background"]:
            used_bgs.add((new_sc["background"], new_sc["timezone"]))

        for pos in ("left", "center", "right"):
            ac = sc.get(pos)
            if not isinstance(ac, dict):
                continue
            aid_val = ac.get("id")
            if aid_val is None:
                continue
            aid = int(aid_val)
            if aid <= 0:
                continue
            used_actors.add(aid)

            # Determine active & display_name: center/single actor takes normalized speaker
            ac_speaker = speaker
            if "display_name" in ac and ac["display_name"] and not KANA_RE.search(str(ac["display_name"])):
                ac_speaker = str(ac["display_name"])
            if pos != "center" and "active" in ac:
                is_active = bool(ac["active"])
            else:
                is_active = speaker != ""

            ac_type = str(ac.get("type", ""))
            if aid in NAVI_IDS:
                ac_type = "navi"

            new_sc[pos] = {
                "id": aid,
                "key": ac_speaker if ac_speaker else str(ac.get("key", "")),
                "display_name": ac_speaker,
                "expression": str(ac.get("expression") or "tujo"),
                "emotion": str(ac.get("emotion", "")),
                "active": is_active,
                "appearance": str(ac.get("appearance", "")),
                "type": ac_type,
                "face_id": str(ac.get("face_id", "")),
            }

        new_scenes.append(new_sc)

    old_xys = raw_doc.get("actress_xys", {})
    actress_xys: dict[str, dict] = {}
    asset_names_set: set[str] = set()
    for n in raw_doc.get("asset_names", []):
        if isinstance(n, str) and n:
            asset_names_set.add(n)
    for aid in sorted(used_actors):
        asset_names_set.add(f"storyactress/storyactress{aid}")
        xy_key = f"actress_xy_{aid}"
        if isinstance(old_xys.get(xy_key), dict):
            merged = default_xy()
            for k, v in old_xys[xy_key].items():
                if isinstance(v, dict):
                    merged[k] = v
            actress_xys[xy_key] = merged
        else:
            actress_xys[xy_key] = default_xy()
    for bg, tz in sorted(used_bgs):
        asset_names_set.add(f"storybackground/storybackground{bg}_{tz}")

    out_doc = {
        "story": {
            "id": ep["id"],
            "chapter_id": ep["chapter_id"],
            "order_num": ep["order_num"],
            "title": ep["title"],
            "sub_title": ep["sub_title"],
            "area_id": ep.get("area_id", 0),
            "quest_id": ep.get("quest_id", 0),
            "read_timing_id": ep.get("read_timing_id", 1),
            "story_type_id": ep.get("story_type_id", 1),
            "scenes_map_array": new_scenes,
        },
        "asset_names": sorted(asset_names_set),
        "actress_xys": actress_xys,
    }
    return out_doc, out_doc["asset_names"], warnings


def main() -> int:
    repo_root = Path(__file__).resolve().parents[2]
    parser = argparse.ArgumentParser(description="Extract and normalize CN3.5 main stories from Story APK")
    parser.add_argument(
        "--apk",
        type=Path,
        default=None,
        help="Read-only path to player story APK (defaults to inputs/apk/ or workspace parent)",
    )
    parser.add_argument(
        "--catalog",
        type=Path,
        default=repo_root / "server/tools/cn350_main_story_catalog.json",
        help="Main story catalog & speaker translation map JSON",
    )
    parser.add_argument(
        "--assets-dir",
        type=Path,
        default=repo_root / "server/assets/cn350",
        help="CN3.5 assets directory containing serve_asset_info_android4_v2_short.txt",
    )
    parser.add_argument(
        "--extra-unity-dir",
        type=Path,
        default=repo_root / "inputs/files",
        help="Overlay unity3d directory for full asset closure verification",
    )
    parser.add_argument(
        "--out",
        type=Path,
        default=repo_root / "server/assets/cn350/stories",
        help="Output stories directory (gitignored)",
    )
    args = parser.parse_args()

    if args.apk is None:
        args.apk = repo_root / "inputs/apk/梅露可-cn350-剧情版.apk"

    if not args.apk.is_file():
        print(f"ERROR: APK not found: {args.apk} (pass --apk <path>)", file=sys.stderr)
        return 1

    catalog = json.loads(args.catalog.read_text(encoding="utf-8"))
    speaker_map: dict[str, str] = catalog["speaker_map"]
    chapters: list[dict] = catalog["chapters"]
    episodes: list[dict] = catalog["episodes"]

    base_assets, overlay_assets = load_accepted_assets(args.assets_dir, args.extra_unity_dir)

    args.out.mkdir(parents=True, exist_ok=True)
    for old in args.out.glob("story_*.json"):
        old.unlink()

    index_stories: list[dict] = []
    missing_episodes: list[int] = []
    asset_errors: list[str] = []
    speaker_warnings: list[str] = []
    base_ok_count = 0

    with zipfile.ZipFile(args.apk, "r") as zf:
        apk_idx = json.loads(zf.read("assets/backend/cn350/stories/index.json"))
        oid_to_entry: dict[int, dict] = {}
        for entry in apk_idx.get("stories", []):
            m = TEXTASSET_RE.match(entry.get("textasset", ""))
            if m:
                oid_to_entry[int(m.group(1))] = entry

        for ep in episodes:
            sid = ep["id"]
            apk_entry = oid_to_entry.get(sid)
            if not apk_entry:
                missing_episodes.append(sid)
                continue
            apk_story_path = f"assets/backend/cn350/stories/story_{apk_entry['id']}.json"
            raw_doc = json.loads(zf.read(apk_story_path))
            out_doc, asset_names, warns = normalize_story_doc(raw_doc, ep, speaker_map)
            speaker_warnings.extend(warns)

            missing_in_overlay = [a for a in asset_names if a not in overlay_assets]
            if missing_in_overlay:
                asset_errors.append(f"story_{sid}: missing assets {missing_in_overlay}")
                continue
            if all(a in base_assets for a in asset_names):
                base_ok_count += 1

            out_file = args.out / f"story_{sid}.json"
            out_file.write_text(
                json.dumps(out_doc, ensure_ascii=False, separators=(",", ":")),
                encoding="utf-8",
            )
            index_stories.append(
                {
                    "id": sid,
                    "textasset": f"StoryYamlData_{sid}",
                    "source_file": f"StoryYamlData_{sid}.json",
                    "source_sha256": apk_entry.get("source_sha256", ""),
                    "chapter_id": ep["chapter_id"],
                    "order_num": ep["order_num"],
                    "title": ep["title"],
                    "sub_title": ep["sub_title"],
                    "area_id": ep.get("area_id", 0),
                    "quest_id": ep.get("quest_id", 0),
                    "read_timing_id": ep.get("read_timing_id", 1),
                    "story_type_id": ep.get("story_type_id", 1),
                    "scene_count": len(out_doc["story"]["scenes_map_array"]),
                    "provenance": apk_entry.get(
                        "provenance",
                        "MercStoria Memorial CHS / StoryYamlData (AES-256-CBC recovered)",
                    ),
                }
            )

    index_doc = {
        "chapters": chapters,
        "stories": index_stories,
        "excluded": [],
    }
    (args.out / "index.json").write_text(
        json.dumps(index_doc, ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8",
    )

    print(f"Extracted chapters: {len(chapters)}")
    print(f"Extracted main episodes: {len(index_stories)} / {len(episodes)}")
    print(f"Base asset_info pass: {base_ok_count}, Overlay full pass: {len(index_stories)}")
    print(f"Missing episodes: {len(missing_episodes)}")
    print(f"Asset errors: {len(asset_errors)}")
    print(f"Residual kana speaker warnings: {len(speaker_warnings)}")
    if missing_episodes or asset_errors or speaker_warnings:
        for msg in asset_errors[:10] + speaker_warnings[:10]:
            print("  !", msg, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
