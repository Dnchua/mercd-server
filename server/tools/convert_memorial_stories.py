#!/usr/bin/env python3
"""Convert structured Memorial CHS StoryYamlData into CN3.5 story fixtures.

Plan: plans/cn35-story-memorial-port-2026-11-20/PLAN.md (stage 2).

Inputs (all read-only, produced outside this repo by the extraction project):
  --scenes       <extract>/output/analysis/story/scenes/        (parse_story_yaml.py)
  --catalog      <extract>/output/analysis/story/story_catalog.json (parse_story_master.py)
  --gap-report   <extract>/output/analysis/story/gap_report.json   (story_gap_report.py)
  --main-catalog server/tools/cn350_main_story_catalog.json      (CN3.5 official 533 ids/titles, speaker_map)

Scope (D5/D6): stories whose gap_report scope is "in" or "boundary"; "out" is never written.
Titles (D7): the 533 main episodes keep the CN3.5 official title/sub_title/area/quest metadata.
Body text and speaker names come from Memorial CHS (already Simplified Chinese).

Scene mapping (Memorial -> CN3.5 SceneDocument / ActressDocument):
  left/center/right      -> same position; texture_id -> id; DisplayName -> key/display_name;
                            Navi -> type "navi"; Scout -> type "scoutactress" (unit/unit_large_ns_<id>)
  speakers[]             -> speaker (scene DisplayName > corpus key->DisplayName > speaker_map)
  bgm.name               -> bgm "sound/<name>" (+ asset_names)
  effect                 -> CN-supported names pass through; Fade -> Wipe; others dropped
  window_type/text_size  -> "shout" / "big" | "middle"
Characters whose bundle cannot be closed (no storyactress / unit_large_ns) are dropped from the
scene (text kept) and recorded in the report.

Output (gitignored): --out (default server/assets/cn350/stories/) story_<id>.json + index.json,
plus convert_report.json with drop/degrade statistics.
"""

from __future__ import annotations

import argparse
import collections
import json
import os
import re
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from extract_cn350_stories import load_accepted_assets, normalize_speaker  # noqa: E402

# 平假名 + 片假名本体；不含「・」(U+30FB)「ー」(U+30FC)，纪念版中文译名常用「・」
KANA_RE = re.compile(r"[\u3041-\u3096\u30a1-\u30fa]")
CN_EFFECTS = {"Attack", "Earthquake", "Heal", "Shake", "Summon", "Wipe", "InputName"}
EFFECT_MAP = {"Fade": "Wipe"}
UNIT_CHAPTER_ID = 3
STORY_TYPE_ID = {"メイン第一部オリジン": 1, "ユニット": 2, "イベント": 4}


def default_xy() -> dict:
    return {"xy": {"x": 0, "y": 0}, "face_xy": {"x": 0, "y": 0}, "emotion_xy": {"x": 0, "y": 0}}


def clean_text(t: str | None) -> str:
    t = (t or "").replace("\r\n", "\n").replace("\r", "\n").strip()
    return t if len(t) >= 2 else "「」"


class Converter:
    def __init__(self, accepted: set[str], speaker_map: dict[str, str], key_names: dict[str, str]):
        self.accepted = accepted
        self.speaker_map = speaker_map
        self.key_names = key_names
        self.stats = collections.Counter()
        self.dropped_actors = collections.Counter()
        self.unresolved_speakers = collections.Counter()

    def zh(self, key: str, scene_names: dict[str, str]) -> str:
        if key in ("", "<name>"):
            return key
        name = scene_names.get(key) or self.key_names.get(key)
        if name and not KANA_RE.search(name):
            return name
        mapped = normalize_speaker(key, self.speaker_map)
        if mapped != key or not KANA_RE.search(key):
            return mapped
        self.unresolved_speakers[key] += 1
        return key

    def actor(self, ch: dict, scene_names: dict[str, str], speaking: set[str]):
        tid = ch["texture_id"]
        if ch["type"] == "Scout":
            bundle, typ = f"unit/unit_large_ns_{tid}", "scoutactress"
        else:
            bundle, typ = f"storyactress/storyactress{tid}", "navi" if ch["type"] == "Navi" else ""
        if bundle not in self.accepted:
            self.dropped_actors[f"{bundle}"] += 1
            self.stats["actor_dropped"] += 1
            return None, None
        name = self.zh(ch["key"] or "", scene_names)
        if ch["display_name"] and not KANA_RE.search(ch["display_name"]):
            name = ch["display_name"]
        self.stats["actor_" + (typ or "default")] += 1
        return {
            "id": tid, "key": name, "display_name": name, "expression": "tujo", "emotion": "",
            "active": bool(ch["active"]), "appearance": "", "type": typ, "face_id": "",
        }, bundle

    def convert(self, meta: dict, parts: list[dict]) -> dict:
        scenes_out, assets, actors = [], set(), set()
        for part in parts:
            for sc in part["scenes"]:
                chars = {p: sc[p] for p in ("left", "center", "right") if sc[p] and sc[p]["texture_id"]}
                scene_names = {c["key"]: c["display_name"] for c in chars.values()
                               if c["key"] and c["display_name"] and not KANA_RE.search(c["display_name"])}
                speakers = [s for s in (sc["speakers"] or []) if s]
                speaker = "・".join(self.zh(s, scene_names) for s in speakers)
                effect = EFFECT_MAP.get(sc["effect"], sc["effect"])
                if effect == "None":
                    effect = ""
                if effect and effect not in CN_EFFECTS:
                    self.stats[f"effect_dropped:{effect}"] += 1
                    effect = ""
                elif sc["effect"] in EFFECT_MAP:
                    self.stats[f"effect_mapped:{sc['effect']}->{effect}"] += 1
                bgm = ""
                if sc["bgm"] and sc["bgm"]["name"]:
                    b = "sound/" + sc["bgm"]["name"]
                    if b in self.accepted:
                        bgm = b; assets.add(b)
                    else:
                        self.stats["bgm_dropped"] += 1
                bg, tz = sc["background"] or "", str(sc["timezone"])
                if bg:
                    assets.add(f"storybackground/storybackground{bg}_{tz}")
                new = {
                    "speaker": speaker,
                    "text": clean_text(sc["text"]),
                    "background": bg,
                    "timezone": tz,
                    "effect": effect,
                    "text_size": {"Big": "big", "Middle": "middle"}.get(sc["text_size"], ""),
                    "window_type": "shout" if sc["window_type"] == "Shout" else "",
                    "bgm": bgm,
                    "effect_parameter": "",
                }
                for pos, ch in chars.items():
                    doc, bundle = self.actor(ch, scene_names, set(speakers))
                    if doc:
                        new[pos] = doc; assets.add(bundle); actors.add(ch["texture_id"])
                for k in ("sound_effects", "asset_parameters"):
                    if sc[k]:
                        self.stats[f"{k}_dropped_scenes"] += 1
                for pos, ch in chars.items():
                    if ch["face_texture_id"]:
                        self.stats["face_texture_dropped"] += 1
                scenes_out.append(new)
                self.stats["scenes"] += 1
        return {
            "story": {**{k: meta[k] for k in ("id", "chapter_id", "order_num", "title", "sub_title", "area_id",
                                              "quest_id", "read_timing_id", "story_type_id")},
                      "scenes_map_array": scenes_out},
            "asset_names": sorted(assets),
            "actress_xys": {f"actress_xy_{a}": default_xy() for a in sorted(actors)},
        }


def main() -> int:
    repo = Path(__file__).resolve().parents[2]
    extract = Path.home() / "Desktop/个人工作区/梅露可物语工程资源提取/output/analysis/story"
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--scenes", type=Path, default=extract / "scenes")
    ap.add_argument("--catalog", type=Path, default=extract / "story_catalog.json")
    ap.add_argument("--gap-report", type=Path, default=extract / "gap_report.json")
    ap.add_argument("--main-catalog", type=Path, default=repo / "server/tools/cn350_main_story_catalog.json")
    ap.add_argument("--assets-dir", type=Path, default=repo / "server/assets/cn350")
    ap.add_argument("--extra-unity-dir", type=Path, action="append",
                    help="overlay unity3d dirs (remote_assets / client/decoded/assets/unity); repeatable")
    ap.add_argument("--out", type=Path, default=repo / "server/assets/cn350/stories")
    a = ap.parse_args()
    overlays = a.extra_unity_dir or [repo / "client/decoded/assets/unity"]

    accepted: set[str] = set()
    for d in overlays:
        _, ov = load_accepted_assets(a.assets_dir, d)
        accepted |= ov

    main_cat = json.loads(a.main_catalog.read_text(encoding="utf-8"))
    main_eps = {e["id"]: e for e in main_cat["episodes"]}
    cat = json.loads(a.catalog.read_text(encoding="utf-8"))
    chapters_mem = {c["id"]: c for c in cat["chapters"]}
    report = json.loads(a.gap_report.read_text(encoding="utf-8"))
    scope = {s["story_id"]: s["scope"] for s in report["stories"]}

    parts_by_id: dict[int, list[dict]] = collections.defaultdict(list)
    key_votes: dict[str, collections.Counter] = collections.defaultdict(collections.Counter)
    for f in os.listdir(a.scenes):
        d = json.loads((a.scenes / f).read_text(encoding="utf-8"))
        parts_by_id[d["id"]].append(d)
        for sc in d["scenes"]:
            for p in ("left", "center", "right"):
                ch = sc[p]
                if ch and ch["key"] and ch["display_name"] and not KANA_RE.search(ch["display_name"]):
                    key_votes[ch["key"]][ch["display_name"]] += 1
    for v in parts_by_id.values():
        v.sort(key=lambda d: d["part"] or 0)
    key_names = {k: c.most_common(1)[0][0] for k, c in key_votes.items()}

    conv = Converter(accepted, main_cat["speaker_map"], key_names)

    # ---- choose stories & metadata ----
    metas: list[dict] = []
    by_chapter: dict[int, list[dict]] = collections.defaultdict(list)
    for s in cat["stories"]:
        if scope.get(s["story_id"]) not in ("in", "boundary"):
            continue
        by_chapter[UNIT_CHAPTER_ID if s["type"] == "ユニット" else s["chapter_id"]].append(s)
    for cid, ss in by_chapter.items():
        ss.sort(key=lambda s: (s["order"], s["story_id"]))
        for i, s in enumerate(ss, 1):
            sid = s["story_id"]
            if sid in main_eps:
                e = main_eps[sid]
                meta = {k: e.get(k, 0) for k in ("chapter_id", "order_num", "title", "sub_title", "area_id",
                                                 "quest_id", "read_timing_id", "story_type_id")}
                meta["id"] = sid
            else:
                meta = {"id": sid, "chapter_id": cid, "order_num": i, "title": s["title"],
                        "sub_title": s["sub_title"] or "", "area_id": 0, "quest_id": 0, "read_timing_id": 1,
                        "story_type_id": STORY_TYPE_ID.get(s["type"], 1)}
            meta["_type"] = s["type"]
            metas.append(meta)

    # ---- chapters (main from CN catalog, then 同伴故事, then events by memorial order) ----
    chapters = [dict(c) for c in main_cat["chapters"]]
    if by_chapter.get(UNIT_CHAPTER_ID):
        chapters.append({"id": UNIT_CHAPTER_ID, "name": "同伴故事", "background_asset_name": "9s_btnLarge_basic",
                         "provenance": "memorial-chs ChapterMasterData id=3"})
    ev = sorted((c for c in by_chapter if c in chapters_mem and chapters_mem[c]["type"] == "イベント"),
                key=lambda c: chapters_mem[c]["order"])
    for c in ev:
        chapters.append({"id": c, "name": chapters_mem[c]["name"], "background_asset_name": "9s_btnLarge_basic",
                         "provenance": f"memorial-chs ChapterMasterData id={c} event_id={chapters_mem[c]['event_id']}"})

    # ---- convert & write (staging dir, then swap) ----
    stage = repo / "tmp" / "stories.staging"
    stage.mkdir(parents=True, exist_ok=True)
    for old in stage.glob("*.json"):
        old.unlink()
    index_stories, closure_errors = [], []
    for meta in sorted(metas, key=lambda m: m["id"]):
        doc = conv.convert(meta, parts_by_id[meta["id"]])
        missing = [n for n in doc["asset_names"] if n not in accepted]
        if missing:
            closure_errors.append({"id": meta["id"], "missing": missing})
            continue
        (stage / f"story_{meta['id']}.json").write_text(
            json.dumps(doc, ensure_ascii=False, separators=(",", ":")), encoding="utf-8")
        index_stories.append({
            "id": meta["id"], "textasset": f"StoryYamlData_{meta['id']}", "source_file": f"story_{meta['id']}.json",
            "source_sha256": "", **{k: meta[k] for k in ("chapter_id", "order_num", "title", "sub_title", "area_id",
                                                          "quest_id", "read_timing_id", "story_type_id")},
            "scene_count": len(doc["story"]["scenes_map_array"]),
            "provenance": f"MercStoria Memorial CHS StoryYamlData ({meta['_type']}) via parse_story_yaml.py",
        })
    used = {s["chapter_id"] for s in index_stories}
    index = {"chapters": [c for c in chapters if c["id"] in used], "stories": index_stories,
             "excluded": [{"textasset": f"StoryYamlData_{s['story_id']}", "source_file": "", "inner_id": s["story_id"],
                           "reason": "out-of-scope: " + s["reason"]} for s in report["stories"] if s["scope"] == "out"]}
    (stage / "index.json").write_text(json.dumps(index, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")
    rep = {"written": len(index_stories), "closure_errors": closure_errors, "stats": dict(conv.stats),
           "dropped_actor_bundles": dict(conv.dropped_actors.most_common()),
           "unresolved_speakers": dict(conv.unresolved_speakers.most_common()),
           "chapters": len(index["chapters"])}
    (stage / "convert_report.json").write_text(json.dumps(rep, ensure_ascii=False, indent=1), encoding="utf-8")

    if a.out.exists():
        # 旧目录整体移到仓库 tmp/（gitignored）留档，不删除；回退时把它移回 --out 即可
        backup = repo / "tmp" / f"stories-backup-{time.strftime('%Y%m%d-%H%M%S')}"
        backup.parent.mkdir(parents=True, exist_ok=True)
        a.out.rename(backup)
        print(f"previous fixture moved to {backup}")
    stage.rename(a.out)
    print(json.dumps({k: v for k, v in rep.items() if k != "closure_errors"}, ensure_ascii=False, indent=1)[:3000])
    print(f"closure_errors={len(closure_errors)} out={a.out}")
    return 1 if closure_errors else 0


if __name__ == "__main__":
    raise SystemExit(main())
