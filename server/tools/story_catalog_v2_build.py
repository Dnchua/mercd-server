#!/usr/bin/env python3
"""用数值组 story-encounter-v2 方案重写 serve_quest_catalog.json 的主线目录。

输入：
  - 数值仓库 datasets/normalized/cn35-main-story.json（mlk 原服录播，27/108/432）
  - 数值仓库 design/encounters/story-v2/cn35-main-*.json（432 份方案）
  - server/assets/cn350/serve_quest_catalog.json（保留 _doc/execute_defaults 等其余键）

规则（2026-11-23 交接）：
  - stage：原服 id/name/description，不写 asset_name。
  - area：原服 id/name/description/bonus_label/area_image_id；
    area_image_id 101–108 缺资源 → {101:95,102:93,103:93,104:80,105:80,106:93,107:93,108:88}。
  - quest：原服字段 + background_id 81→74、82→58；不写 background_pool。
    battle.exp=normal exp；difficulty 三档 {hp_bp,at_bp,exp,gacha_point,guild_asset}；
    seed_drop 照方案。
  - 硬校验：27/108/432、quest_id 严格递增且与 area 顺序一致、出怪 id 可渲染且
    在 cards+memorial、background_id 必须三时段套件齐全。
"""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path

SERVER = Path(__file__).resolve().parents[1]
REPO = SERVER.parent
NUM_REPO = REPO.parent.parent / "数值维护"

STORY_JSON = NUM_REPO / "datasets" / "normalized" / "cn35-main-story.json"
PLANS_DIR = NUM_REPO / "design" / "encounters" / "story-v2"
CATALOG_PATH = SERVER / "assets" / "cn350" / "serve_quest_catalog.json"
MEMORIAL_PATH = SERVER / "internal" / "cn35game" / "memorial_masterdata.json"
MONSTERS_JSON = SERVER / "assets" / "shared" / "cards" / "monsters.json"

AREA_IMAGE_SUBS = {101: 95, 102: 93, 103: 93, 104: 80, 105: 80, 106: 93, 107: 93, 108: 88}
BACKGROUND_SUBS = {81: 74, 82: 58}
DIFFS = ("normal", "hard", "very_hard")


def bundle_stems(*dirs: Path) -> set[str]:
    out = set()
    for d in dirs:
        if d.is_dir():
            for f in d.iterdir():
                out.add(re.sub(r"\.(Android|iOS)\.unity3d$", "", f.name))
    return out


def fail(msg: str) -> "NoReturn":
    print(f"FAIL: {msg}", file=sys.stderr)
    sys.exit(1)


def main() -> int:
    story = json.loads(STORY_JSON.read_text(encoding="utf-8"))
    catalog = json.loads(CATALOG_PATH.read_text(encoding="utf-8"))
    memorial = json.loads(MEMORIAL_PATH.read_text(encoding="utf-8"))["monster_memorial"]
    cards = {m["id"] for m in json.loads(MONSTERS_JSON.read_text(encoding="utf-8"))}

    monster_bundles = bundle_stems(
        REPO / "client" / "decoded" / "assets" / "unity" / "monster",
        REPO / "inputs" / "files" / "monster",
    )
    bg_bundles = bundle_stems(
        REPO / "client" / "decoded" / "assets" / "unity" / "background",
        REPO / "inputs" / "files" / "background",
    )
    area_bundles = bundle_stems(
        REPO / "client" / "decoded" / "assets" / "unity" / "area",
        REPO / "inputs" / "files" / "area",
    )
    valid_bg = {
        int(m.group(1))
        for b in bg_bundles
        if (m := re.fullmatch(r"background(\d+)", b.removesuffix("_1")))
        and f"{b[:-2]}_2" in bg_bundles and f"{b[:-2]}_3" in bg_bundles
    }

    # 替代图存在性硬校验（缺失即停）。
    for v in set(AREA_IMAGE_SUBS.values()):
        if f"area_{v}" not in area_bundles:
            fail(f"area_image 替代图 area_{v} 不存在")
    for k, v in BACKGROUND_SUBS.items():
        if v not in valid_bg:
            fail(f"background 替代套件 {v}（<-{k}）三时段不齐")

    plans: dict[int, dict] = {}
    for f in sorted(PLANS_DIR.glob("cn35-main-*.json")):
        p = json.loads(f.read_text(encoding="utf-8"))
        plans[p["game_id"]] = p

    stages, areas, quests = story["stages"], story["areas"], story["quests"]
    if (len(stages), len(areas), len(quests)) != (27, 108, 432):
        fail(f"输入计数 {(len(stages), len(areas), len(quests))} != (27,108,432)")

    area_stage = {a["id"]: a["stage_id"] for a in areas}
    x_subs: dict[str, list] = {}
    out_quests = []
    prev_id = 0
    prev_area = 0
    for q in quests:
        qid = q["id"]
        if qid <= prev_id:
            fail(f"quest_id 非严格递增: {qid}")
        if q["area_id"] < prev_area:
            fail(f"quest {qid} area_id 顺序回退: {q['area_id']} < {prev_area}")
        prev_id, prev_area = qid, q["area_id"]

        p = plans.get(qid)
        if p is None:
            fail(f"quest {qid} 缺 v2 方案")
        if p["wave_conf"] != q["wave_conf"]:
            fail(f"quest {qid} 方案 wave_conf 与原服不一致")

        bg = BACKGROUND_SUBS.get(q["background_id"], q["background_id"])
        if bg not in valid_bg:
            fail(f"quest {qid} background_id {bg}（原 {q['background_id']}）无三时段套件")

        n_entries = 0
        for wv in p["appear_table"].values():
            for e in wv.values():
                if "id" not in e:
                    if e != {"type": "boss_appear"}:
                        fail(f"quest {qid} 出现非 boss_appear 无 id 出怪项")
                    continue
                mid = e["id"]
                n_entries += 1
                if not {f"monster_{mid}", f"monster_btn_{mid}", f"monster_large_ns_{mid}"} <= monster_bundles:
                    fail(f"quest {qid} 魔宠 {mid} 三件套不可渲染")
                if str(mid) not in memorial or mid not in cards:
                    fail(f"quest {qid} 魔宠 {mid} 不在 memorial/cards")
        if n_entries == 0:
            fail(f"quest {qid} 无出怪项")

        prof = p["difficulty_profiles"]
        seed_id = prof["normal"]["seed_drop_monster_id"]
        difficulty = {}
        chance = {}
        for d in DIFFS:
            difficulty[d] = {
                "hp_bp": round(prof[d]["hp_multiplier"] * 1000),
                "at_bp": round(prof[d]["at_multiplier"] * 1000),
                "exp": prof[d]["experience"],
                "gacha_point": prof[d]["gold"],
                "guild_asset": prof[d]["guild_asset"],
            }
            chance[d] = prof[d]["seed_drop_chance_bp"]
            if prof[d]["seed_drop_monster_id"] != seed_id:
                fail(f"quest {qid} 三档 seed_drop_monster_id 不一致")

        doc = {
            "id": qid,
            "area_id": q["area_id"],
            "name": q["name"],
            "attr_id": str(q["attr_id"]),
            "mode": "quest",
            "monster_label": q["monster_label"] or "",
            "boss_label": q["boss_label"] or "",
            "wave_count": q["wave_count"],
            "wave_conf": q["wave_conf"],
            "use_ap_map": q["use_ap_map"],
            "weapon_rate_hash": q["weapon_rate_hash"],
            "initial_power": q["initial_power"],
            "unit_count_limit": q["unit_count_limit"],
            "seed_atack_rate": q["seed_atack_rate"],
            "unit_help_rate": q["unit_help_rate"],
            "continuable": q["continuable"],
            "save_current_quest": True,
            "current_quest_asset_name": "common/icon-current_quest_normal",
            "help_base_sec": q["help_base_sec"],
            "help_sec_level_rate": q["help_sec_level_rate"],
            "difficulty_names": q["difficulty_names"],
            "background_id": bg,
            "battle": {
                "exp": prof["normal"]["experience"],
                "appear_table": p["appear_table"],
                "difficulty": difficulty,
                "seed_drop": {"monster_id": seed_id, "chance_bp": chance},
            },
            "x_record_key": p["record_key"],
        }
        if p.get("substitutions"):
            x_subs[str(qid)] = p["substitutions"]
        out_quests.append(doc)

    out_areas = []
    for a in areas:
        img = AREA_IMAGE_SUBS.get(a["area_image_id"], a["area_image_id"])
        out_areas.append({
            "id": a["id"],
            "stage_id": a["stage_id"],
            "name": a["name"],
            "description": a["description"],
            "bonus_label": a["bonus_label"],
            "area_image_id": img,
        })
        if f"area_{img}" not in area_bundles:
            fail(f"area {a['id']} area_image_id {img} 资源缺失")

    out_stages = [
        {"id": s["id"], "name": s["name"], "description": s["description"]}
        for s in stages
    ]

    catalog["stages"] = out_stages
    catalog["areas"] = out_areas
    catalog["quests"] = out_quests
    catalog["_provenance"] = (
        "主线 stage/area/quest 元数据 = mlk 原服录播（/stages/ + /areas/，CONFIRMED，"
        "数值仓库 datasets/normalized/cn35-main-story.json）；battle/appear_table/"
        "difficulty/seed_drop = 数值组 story-encounter-v2 方案（RECONSTRUCT 草案，"
        "experiments/story_encounter_v2.py 生成，2026-11-23）。area_image_id 101–108 "
        "与 background_id 81/82 本地缺资源按映射替换（x_*_substitutions）。"
        "execute_defaults 段仍为 quest0.json 采样常量。"
    )
    catalog.pop("_provenance_story1", None)
    catalog.pop("x_substitutions", None)
    catalog["x_substitutions_v2"] = x_subs
    catalog["x_background_substitutions"] = {str(k): v for k, v in BACKGROUND_SUBS.items()}
    catalog["x_area_image_substitutions"] = {str(k): v for k, v in AREA_IMAGE_SUBS.items()}

    CATALOG_PATH.write_text(
        json.dumps(catalog, ensure_ascii=False, indent=1) + "\n", encoding="utf-8"
    )
    print(f"OK: 27 stages / 108 areas / 432 quests, "
          f"{len(x_subs)} 关含魔宠替换 -> {CATALOG_PATH}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
