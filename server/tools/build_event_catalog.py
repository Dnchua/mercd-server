#!/usr/bin/env python3
"""Generate server/assets/cn350/serve_event_catalog.json (CN3.5 M8).

M8 「限时活动与周常探险」静态目录生成器。数据来源与证据等级见
docs/17-限时活动与周常探险回放统计.md：

  - origin        ：mlk-capture-201711 原服录播逐字段观测（下方常量即摘录）。
  - announcement  ：录播 preset_data 公告正文。
  - reconstruction：本脚本按 plans/cn35-m8-events-daily-2026-11-19/PLAN.md §4
                    的规则推导出的重建值。

输出确定性（无随机数）；重复运行字节一致。可选 ``--check`` 只校验现有文件
与生成结果一致（CI/自测用）。录播本身不入库，本脚本只内嵌 doc17 已公开
统计的数值。
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]
OUT = REPO / "server" / "assets" / "cn350" / "serve_event_catalog.json"

# ---------------------------------------------------------------------------
# 1. 历史活动列表（/events/，origin：doc17 §3.2，按下发顺序）
# ---------------------------------------------------------------------------
PAST_EVENTS = [
    (1, "远古女王和石头守卫", "areas_1001"),
    (2, "灾难魔女和天才差生", "areas_1002"),
    (3, "幻惑森林和温柔战神", "areas_1003"),
    (5, "黎明之王和被囚的新娘", "areas_1005"),
    (6, "碧翼海盗和大海雏鸟", "areas_1006"),
    (7, "相克的领主与梦寐之倾城", "areas_1007"),
    (8, "君之梦影与忘却之镇", "areas_1008"),
    (10, "憧憬的追恋人和恋爱狂想曲", "areas_1010"),
    (11, "流星王子与孤单公主", "areas_1011"),
    (13, "调皮妖精与食春之龙", "areas_1013"),
    (14, "乡愁之灯与零散的齿轮", "areas_1014"),
    (15, "平庸的调石师与孤高的烈刃", "areas_1015"),
    (16, "新手猎人与孤旅的赏金猎手", "areas_1016"),
    (25, "繁星散乱，晴转青蛙的学园祭！", "areas_1025"),
    (100000, "讨厌的胡萝卜", "areas_100000"),
    (100004, "双生姐妹和寡言骑士", "areas_100002"),
    (100005, "新春佳节和庙会的邂逅", "areas_100003"),
    (100008, "等爱少女与迟到的信", "areas_100006"),
    (100011, "囚牢之镇的来客与垂翼鸟", "areas_100009"),
    (100013, "消逝的片翼与十字剑之誓", "areas_100013"),
    (17, "无法飞翔的天使和祈祷的圣翼", "areas_1017"),
    (100015, "追逐世事的理想家与点滴的意气", "areas_100015"),
    (19, "红祸之星与优焰的煌宗", "areas_1019"),
    (100016, "黄金盛典与时间停止的少女", "areas_100016"),
    (20, "狩猎千龙的兽与竞奔的龙骑手们", "areas_1020"),
    (100017, "缥缈之梦与约定的钟声", "areas_100018"),
    (21, "奇迹的制造法与爱的逻辑", "areas_1021"),
    (23, "创世之种与根源的森树", "areas_1023"),
    (100018, "失落千年的高傲与孤独的行路者", "areas_100019"),
    (26, "盛馔之神与饿肚子怪", "areas_1026"),
    (100019, "失坠的回忆与镜像之迷宫", "areas_100020"),
    (27, "歌乐坛之王与鸟笼的歌姬", "areas_1027"),
    (29, "紫燐灰的诱惑和柔软的脚枷", "areas_1029"),
    (30, "追忆的海市蜃楼与宿于天地之物", "areas_1030"),
    (31, "冰睑之魔女和不断融化的谎言", "areas_1031"),
    (33, "Miss少年和蛋中所做的梦", "areas_1033"),
    (34, "瓶子和水桶和普普通通的故事", "areas_1034"),
    (36, "舞蹈于宵樱的鬼和平息的旋律", "areas_1036"),
    (37, "黑骨的尊岩和多彩的菜肴", "areas_1037"),
    (38, "灿光之街与阴奔之丝", "areas_1038"),
    (39, "霞影的剧场、盛开于废铁之上的灯火", "areas_1039"),
    (40, "虚逢的假面舞会与恋爱协骚诗", "areas_1040"),
]

# ---------------------------------------------------------------------------
# 2. 公共常量（origin：areas_event / quests_daily 录播）
# ---------------------------------------------------------------------------
POWERUP_CONF = {
    str(i + 1): {"rate": round(1 + 0.3 * i, 1), "next": 150 * (i + 1)} for i in range(11)
}
WEAPON_KEYS = ["slash_rate", "rush_rate", "hit_rate", "arrow_rate", "magic_rate", "gun_rate", "heal_rate"]


def flat_weapon(v=1.0):
    return {k: v for k in WEAPON_KEYS}


def weapon_hash(strong):
    """strong=强势武器键列表；空=全部 1.0（origin：doc17 §4.5）。"""
    out = {}
    for diff, up, down in (("normal", 1.3, 0.85), ("hard", 1.4, 0.8), ("very_hard", 1.5, 0.75)):
        row = {}
        for k in WEAPON_KEYS:
            if k == "heal_rate" or not strong:
                row[k] = 1.0
            else:
                row[k] = up if k in strong else down
        out[diff] = row
    return out


def spawn_times(n, group=2):
    """录播节奏：每 6000ms 一组、组内间隔 250ms，首组 2000ms。"""
    out = []
    g = 0
    while len(out) < n:
        for j in range(group):
            if len(out) >= n:
                break
            out.append(2000 + 6000 * g + 250 * j)
        g += 1
    return out


# ---------------------------------------------------------------------------
# 3. 当期活动 44（origin 元数据：doc17 §4.3–4.5；battle 段为重建值）
# ---------------------------------------------------------------------------
EVENT44_AREAS = [
    # id, 名称, 简介, bonus_label, 原服 area_image_id, 替代 area_image_id
    (4176, "魔菇大街", "连接村庄与外界的通道。用于搬运货物。", "完成率30%会得到礼物钻石 1个&夏尔梅哦！", 4164),
    (4177, "寄宿学校", "由大树建造而成的寄宿学校。", "完成率30%会得到礼物钻石 1个哦！", 4165),
    (4178, "螺旋之间", "大树之间的树木中制造的空间。", "完成率30%会得到礼物钻石 1个哦！", 4162),
]
AREA_WEAPON = {4176: ["rush_rate", "magic_rate"], 4177: [], 4178: ["hit_rate", "gun_rate"]}
# id, 名称, area, normal AP, attr, 怪物标签, Boss 标签, 波数, 原服背景
EVENT44_QUESTS = [
    (1440, "寄宿学校", 4176, 5, 3, "琼卡、波彭 其他", None, 3, 85),
    (1441, "邮局", 4176, 5, 5, "琼卡、波彭 其他", None, 3, 85),
    (1442, "月下美人", 4176, 6, 4, "琼卡、波彭 其他", None, 4, 84),
    (1443, "游戏的传闻", 4176, 7, 1, None, "迫伦斯", 1, 84),
    (1444, "消失的信", 4177, 7, 4, "琼卡、波彭 其他", None, 3, 85),
    (1445, "银色萤火虫", 4177, 8, 2, "琼卡、波彭 其他", None, 3, 84),
    (1446, "水中", 4177, 9, 5, "琼卡、波彭 其他", None, 4, 85),
    (1447, "熬制的花朵与烤制的点心", 4177, 9, 3, "琼卡、波彭 其他", None, 4, 85),
    (1448, "灰色的孩子与季节的螺旋", 4178, 10, 1, "琼卡、波彭 其他", None, 4, 85),
    (1449, "采花贼", 4178, 11, 3, "琼卡、波彭 其他", None, 4, 85),
    (1450, "翅膀的壳", 4178, 11, 2, None, "斯内兰", 1, 85),
    (1451, "蜕变", 4178, 12, 4, "琼卡、波彭 其他", None, 5, 85),
]
# 重建：背景 84/85 本地缺资源 → 统一替代为 background5（周常同款，资源齐全）。
EVENT_BG_SUBSTITUTE = 5
# 重建：琼卡 306（火，资源齐全）+「波彭」替代 + 「其他」同属性普通魔宠。
EVENT_POOL = {
    1: [306, 230, 4, 10],   # 火：琼卡 / 拉比莱奥 / 品奇 / 特洛普
    2: [306, 236, 69, 81],  # 水：琼卡 / 莫古 / 海吉 / 莫诺巴特
    3: [306, 232, 54, 70],  # 风：琼卡 / 拉比莱奥 / 品奇 / 海吉
    4: [306, 364, 79, 146],  # 光：琼卡 / 兹布缇 / 特洛普 / 弗萨萨比
    5: [306, 365, 72, 84],  # 暗：琼卡 / 兹布缇 / 海吉 / 莫诺巴特
}
POPON_SUBSTITUTE = {1: 230, 2: 236, 3: 232, 4: 364, 5: 365}
# 重建：Boss 替代模板（本地有模板且资源齐全、属性一致）。
BOSS_SUBSTITUTE = {1443: (36, "杰伯沃", "迫伦斯"), 1450: (254, "奥鲁加斯", "斯内兰")}

# 区域完成率奖励（PLAN §3.4）：(area, threshold, presents, source)
DIAMOND = {"content_type": "coin", "content_id": 1, "amount": 1, "label": "钻石 1个"}
SHARME = {"content_type": "unit", "content_id": 1274, "amount": 1, "label": "透翅的术士 夏尔梅"}
KIRI = {"content_type": "unit", "content_id": 1275, "amount": 1, "label": "透翅的水射手 琪莉"}
AREA_REWARDS = [
    (4176, 30, [DIAMOND, SHARME], "bonus_label+announcement"),
    (4176, 60, [SHARME], "announcement"),
    (4176, 100, [DIAMOND], "reconstruction"),
    (4177, 30, [DIAMOND], "bonus_label"),
    (4177, 60, [KIRI], "announcement"),
    (4177, 100, [SHARME], "announcement"),
    (4178, 30, [DIAMOND], "bonus_label"),
    (4178, 60, [DIAMOND], "reconstruction"),
    (4178, 100, [KIRI], "announcement"),
]


def event_strength(ap):
    """重建：按本地主线同 AP 档拟合（AP5 小怪 HP≈1450/AT≈327，AP12 HP≈6.7k/AT≈817）。"""
    hp = round(1450 * 1.24 ** (ap - 5))
    at = round(327 * 1.14 ** (ap - 5))
    return hp, at


def event_battle(qid, ap, attr, waves, boss):
    hp, at = event_strength(ap)
    gold = round(263 * ap / 40)
    asset = round(131 * ap / 40)
    table = {}
    pool = EVENT_POOL[attr]
    if boss:
        boss_id = BOSS_SUBSTITUTE[qid][0]
        entries = [
            (2000, pool[1], 1.3, False, hp, round(at * 0.85), 140),
            (2250, 306, 1.3, False, hp, round(at * 0.85), 140),
            (8000, boss_id, 1.0, True, round(hp * 6.8), at, 400),
            (14000, pool[2], 1.3, False, hp, round(at * 0.85), 140),
            (14250, pool[3], 1.3, False, hp, round(at * 0.85), 140),
        ]
        table["1"] = {
            str(t): {
                "id": mid, "scale": sc, "powerup_rate": 2.5, "gauge": g,
                "hp": h, "at": a,
                "rewards": [
                    {"key": "gacha_point", "count": gold * (3 if g else 1)},
                    {"key": "guild_asset", "count": asset * (3 if g else 1)},
                    {"key": "power", "count": p},
                ],
            }
            for t, mid, sc, g, h, a, p in entries
        }
    else:
        for w in range(1, waves + 1):
            n = min(3 + w, 6)
            times = spawn_times(n)
            wave = {}
            for i, t in enumerate(times):
                mid = pool[(i + w) % len(pool)]
                wave[str(t)] = {
                    "id": mid, "scale": round(1.25 + 0.05 * ((i * 7 + w) % 4), 2),
                    "powerup_rate": 3, "gauge": False,
                    "hp": round(hp * (1 + 0.1 * (w - 1))), "at": round(at * 0.85),
                    "rewards": [
                        {"key": "gacha_point", "count": gold},
                        {"key": "guild_asset", "count": asset},
                        {"key": "power", "count": 140 if mid != 306 else 160},
                    ],
                }
            table[str(w)] = wave
    exp = ap * 32
    return {
        "exp": exp,
        "bgm_name": "sound/B-4",
        "next_page_message": "name=StageSelect&base=Common/Pages/StageSelect&select=4",
        "appear_table": table,
        "difficulty": {
            "normal": {"hp_bp": 1000, "at_bp": 1000, "exp": exp, "gacha_point": 0, "guild_asset": 0},
            "hard": {"hp_bp": 1600, "at_bp": 1600, "exp": exp * 2, "gacha_point": gold * 6, "guild_asset": asset * 6},
            "very_hard": {"hp_bp": 2400, "at_bp": 2400, "exp": exp * 3, "gacha_point": gold * 12, "guild_asset": asset * 12},
            "impossible": {"hp_bp": 2400, "at_bp": 2400, "exp": exp * 3, "gacha_point": gold * 12, "guild_asset": asset * 12},
        },
        "seed_drop": {"monster_id": 306, "chance_bp": {"normal": 300, "hard": 500, "very_hard": 800, "impossible": 800}},
    }


def build_event44():
    areas = [
        {
            "id": aid, "stage_id": 1044, "name": name, "description": desc,
            "area_image_id": img, "bonus_label": label,
            "x_origin": {"area_image_id": aid, "source": "origin", "substituted": "area_image_id 本地缺资源"},
        }
        for aid, name, desc, label, img in EVENT44_AREAS
    ]
    quests = []
    for qid, name, area, ap, attr, mon, boss, waves, bg in EVENT44_QUESTS:
        wave_conf = {}
        if boss:
            wave_conf["1"] = {"length": 600, "scale_powerup_power": 2.5}
        else:
            for w in range(waves):
                wave_conf[str(w + 1)] = {"length": 640 + 60 * w, "scale_powerup_power": 3}
        q = {
            "id": qid, "area_id": area, "name": name, "attr_id": str(attr),
            "wave_count": waves,
            "use_ap_map": {"normal": ap, "hard": ap * 2, "very_hard": ap * 3, "impossible": ap * 3},
            "weapon_rate_hash": weapon_hash(AREA_WEAPON[area]),
            "wave_conf": wave_conf,
            "unit_count_limit": 5,
            "initial_power": 2000 if boss else 1000,
            "seed_atack_rate": 1.0,
            "unit_help_rate": 0.6,
            "continuable": True,
            "mode": "quest",
            "background_id": EVENT_BG_SUBSTITUTE,
            "difficulty_names": {"0": "普通", "1": "困难", "2": "极难", "3": "炼狱"},
            "current_quest_asset_name": "common/icon-current_quest_countryevent",
            "battle": event_battle(qid, ap, attr, waves, boss),
            "x_origin": {"background_id": bg, "source": "origin(元数据)/reconstruction(battle)"},
        }
        if mon:
            q["monster_label"] = mon
        if boss:
            q["boss_label"] = boss
            bid, bname, oname = BOSS_SUBSTITUTE[qid]
            q["x_origin"]["boss_substitute"] = {"origin": oname, "monster_id": bid, "name": bname}
        else:
            q["x_origin"]["popon_substitute"] = POPON_SUBSTITUTE[attr]
        quests.append(q)
    return {
        "id": 44,
        "name": "雨水滴落的秋之壳与月之雫的翅膀",
        "stage_id": 1044,
        "area_key": "areas_1044",
        "default_window": {"open_at": "", "close_at": ""},
        "areas": areas,
        "quests": quests,
        "area_rewards": [
            {"area_id": a, "threshold": th, "presents": p, "source": src} for a, th, p, src in AREA_REWARDS
        ],
    }


# ---------------------------------------------------------------------------
# 4. 周常（origin：周六特级 quest 104 开战包；其余为重建值）
# ---------------------------------------------------------------------------
# 怪物家族：attr(1..5) → monster_id；SP 按种类固定；稀有 1=╂ 2=╂╂（origin）
FAMILIES = {
    "pinqi": {"ids": {1: 4, 2: 53, 3: 54, 4: 55, 5: 56}, "hp": 6402, "at": 3632, "sp": 140, "rare": 1},
    "haiji": {"ids": {1: 8, 2: 69, 3: 70, 4: 71, 5: 72}, "hp": 9680, "at": 3254, "sp": 160, "rare": 1},
    "teluopu": {"ids": {1: 10, 2: 77, 3: 78, 4: 79, 5: 80}, "hp": 6160, "at": 5460, "sp": 150, "rare": 1},
    "naji": {"ids": {1: 20, 2: 117, 3: 118, 4: 83, 5: 84}, "hp": 15598, "at": 5922, "sp": 210, "rare": 2},
    "monuobate": {"ids": {1: 11, 2: 81, 3: 82, 4: 83, 5: 84}, "hp": 10164, "at": 8882, "sp": 200, "rare": 2},
}
ID2FAM = {}
for fam, spec in FAMILIES.items():
    for attr, mid in spec["ids"].items():
        if fam == "naji" and attr in (4, 5):
            continue  # 光/暗无纳吉：已映射到莫诺巴特
        ID2FAM[mid] = (fam, attr)

# 周六特级 104 出场表（origin：doc17 §5.2.2）：(wave, ms, monster_id, scale, gold, asset)
SAT_TOP = [
    (1, 2000, 69, 1.25652, 264, 130), (1, 2250, 69, 1.6151, 262, 130),
    (1, 8000, 4, 1.30416, 262, 132), (1, 8250, 54, 1.29108, 264, 130),
    (1, 8500, 54, 1.25808, 263, 132), (1, 8750, 4, 1.3002, 262, 132),
    (1, 14000, 53, 1.32156, 264, 130), (1, 14250, 4, 1.32312, 262, 130),
    (1, 14500, 4, 1.34076, 264, 130), (1, 14750, 53, 1.33332, 262, 130),
    (2, 2000, 8, 1.29756, 262, 131), (2, 2250, 8, 1.26408, 264, 131),
    (2, 8000, 77, 1.29996, 263, 130), (2, 8250, 77, 1.27716, 264, 132),
    (2, 14000, 70, 1.27008, 262, 131), (2, 14250, 70, 1.25028, 262, 132),
    (2, 20000, 118, 1.2882, 262, 131), (2, 20250, 10, 1.697, 262, 131),
    (2, 26000, 20, 1.31196, 262, 131), (2, 26250, 77, 1.32156, 263, 131),
    (2, 32000, 118, 1.4408, 262, 132), (2, 32250, 78, 1.25256, 262, 130),
    (3, 2000, 78, 1.4744, 264, 130), (3, 2250, 78, 1.31556, 263, 130),
    (3, 8000, 20, 1.31112, 262, 130), (3, 8250, 117, 1.5482, 263, 131),
    (3, 8500, 118, 1.30884, 262, 130), (3, 14000, 10, 1.32876, 262, 131),
    (3, 14250, 81, 1.30896, 262, 130), (3, 14500, 81, 1.31052, 262, 132),
    (3, 14750, 10, 1.33044, 265, 132), (3, 20000, 117, 1.56005, 263, 131),
    (3, 20250, 20, 1.28028, 263, 131), (3, 20500, 117, 1.27572, 262, 130),
    (3, 26000, 11, 1.30908, 262, 131), (3, 26250, 11, 1.67555, 264, 131),
    (3, 26500, 82, 1.34796, 263, 132), (3, 26750, 82, 1.767, 264, 130),
]

DAYS = [
    # wday, 名称, 主题, 元素(素材日), 属性映射(原 attr→当天 attr)
    (0, "周日探险", "gold", 0, {1: 1, 2: 4, 3: 5}),
    (1, "周一探险", "material", 5, None),
    (2, "周二探险", "material", 1, None),
    (3, "周三探险", "material", 2, None),
    (4, "周四探险", "material", 3, None),
    (5, "周五探险", "material", 4, None),
    (6, "周六探险", "gold", 0, {1: 1, 2: 2, 3: 3}),
]
TIERS = [
    # tier, 名称, AP, 每波怪数, HP/AT 倍率, 允许╂╂ 的最早波次(0=不允许), exp, 素材 form, 素材数量
    (1, "初级", 10, (4, 5, 6), 0.25, 0, 200, 1, (2, 3)),
    (2, "中级", 20, (6, 8, 10), 0.45, 3, 400, 2, (2, 3)),
    (3, "高级", 30, (8, 10, 13), 0.70, 1, 600, 3, (1, 2)),
    (4, "特级", 40, (10, 12, 16), 1.00, 1, 800, 3, (2, 4)),
]
DOWNGRADE = {"naji": "pinqi", "monuobate": "teluopu"}


def daily_monster(fam, attr_map, element, orig_attr):
    attr = element if element else attr_map[orig_attr]
    return FAMILIES[fam]["ids"][attr]


def daily_table(wday, theme, element, attr_map, tier):
    tno, _, ap, counts, mult, rare2_from, _, _, _ = tier
    ratio = ap / 40
    gold_factor = 1.0 if theme == "gold" else 1 / 3
    table = {}
    for w in (1, 2, 3):
        rows = [r for r in SAT_TOP if r[0] == w][: counts[w - 1]]
        # 计数超过录播（不会发生：特级即录播上限）时保持录播节奏。
        wave = {}
        for _, ms, mid, scale, gold, asset in rows:
            fam, oattr = ID2FAM[mid]
            if FAMILIES[fam]["rare"] == 2 and (rare2_from == 0 or w < rare2_from):
                fam = DOWNGRADE[fam]
            spec = FAMILIES[fam]
            if wday == 6 and tno == 4:
                out_id = mid  # 锚点：逐只复刻
            else:
                out_id = daily_monster(fam, attr_map, element, oattr)
            wave[str(ms)] = {
                "id": out_id,
                "scale": scale,
                "powerup_rate": scale,
                "gauge": False,
                "hp": round(spec["hp"] * mult),
                "at": round(spec["at"] * mult),
                "rewards": [
                    {"key": "gacha_point", "count": max(1, round(gold * ratio * gold_factor))},
                    {"key": "guild_asset", "count": max(1, round(asset * ratio))},
                    {"key": "power", "count": spec["sp"]},
                ],
            }
        table[str(w)] = wave
    return table


def build_daily():
    days = []
    for wday, dname, theme, element, attr_map in DAYS:
        quests = []
        for tier in TIERS:
            tno, tname, ap, _, _, _, exp, form, (dmin, dmax) = tier
            qid = 80000 + wday * 10 + tno
            table = daily_table(wday, theme, element, attr_map, tier)
            ids = sorted({e["id"] for wv in table.values() for e in wv.values()})
            seed_candidates = [i for i in ids if FAMILIES[ID2FAM.get(i, ("pinqi", 1))[0]]["rare"] == 2] or ids
            seed_id = 81 if (wday == 6 and tno == 4) else seed_candidates[-1]
            drops = []
            if theme == "material":
                drops.append({
                    "seq": 1, "reward_type": "item",
                    "item_key": f"powerup_item_{element + (form - 1) * 5}",
                    "chance_bp": 10000, "min_count": dmin, "max_count": dmax,
                })
            q = {
                "id": qid, "name": dname, "attr_id": str(element) if element else "",
                "wave_count": 3,
                "use_ap_map": {"normal": ap, "hard": ap * 2, "very_hard": ap * 3, "impossible": ap * 3},
                "weapon_rate_hash": {d: flat_weapon() for d in ("normal", "hard", "very_hard")},
                "wave_conf": {str(i): {"length": 640} for i in (1, 2, 3)},
                "unit_count_limit": 5,
                "initial_power": 1000,
                "seed_atack_rate": 1.0,
                "unit_help_rate": 1.0,
                "continuable": True,
                "mode": "quest",
                "background_id": 5,
                "difficulty_names": {str(i): tname for i in range(4)},
                "difficulty_option_name": dname[:2],
                "current_quest_asset_name": "common/icon-current_quest_daily",
                "battle": {
                    "exp": exp,
                    "bgm_name": "sound/B-4",
                    "next_page_message": "name=StageSelect&base=Common/Pages/StageSelect",
                    "appear_table": table,
                    "seed_drop": {"monster_id": seed_id, "chance_bp": {"normal": [2000, 2500, 3000, 3000][tno - 1]}},
                },
                "drops": drops,
                "x_origin": {
                    "source": "origin" if (wday == 6 and tno == 4) else "reconstruction",
                    "origin_quest_id": 104 if (wday == 6 and tno == 4) else (100 + tno if wday == 6 else 0),
                },
            }
            quests.append(q)
        days.append({"wday": wday, "name": dname, "theme": theme, "element": element, "quests": quests})
    return {"switch_tz": "Asia/Shanghai", "switch_hour": 0, "days": days}


def build():
    return {
        "_doc": (
            "CN3.5 M8 限时活动与周常探险静态目录（server/tools/build_event_catalog.py 生成，勿手改）。"
            "past_events=历史活动列表（只开列表，D1）；events=当期活动（按时间窗开放，D2）；"
            "daily=周常 7 天×4 档（quest_id=80000+wday*10+tier，D5）。"
        ),
        "_provenance": (
            "origin=mlk-capture-201711 录播（doc17 §3–§5）；announcement=录播公告；"
            "reconstruction=PLAN §4 重建规则。x_origin 段记录原服值与替代原因，服务端不下发。"
        ),
        "past_events": [{"id": i, "name": n, "area_key": k} for i, n, k in PAST_EVENTS],
        "events": [build_event44()],
        "daily": build_daily(),
    }


def render():
    return json.dumps(build(), ensure_ascii=False, indent=1, sort_keys=False) + "\n"


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--check", action="store_true", help="只校验现有文件与生成结果一致")
    ap.add_argument("--out", type=Path, default=OUT)
    args = ap.parse_args()
    text = render()
    if args.check:
        cur = args.out.read_text(encoding="utf-8") if args.out.exists() else ""
        if cur != text:
            print(f"ERROR: {args.out} 与生成结果不一致，请重新运行 build_event_catalog.py", file=sys.stderr)
            return 1
        print("OK: serve_event_catalog.json 与生成结果一致")
        return 0
    args.out.write_text(text, encoding="utf-8")
    print(f"wrote {args.out}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
