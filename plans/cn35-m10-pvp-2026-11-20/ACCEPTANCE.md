# CN3.5 M10 验收与验证规格（ACCEPTANCE.md）

## 1. 强门禁（任一不满足就不得宣称 M10 完成）

1. **客户端 DLL 零篡改**；原始 APK 按 `client/baseline.json` 校验。
2. **历史迁移只读**：`0001`–`0056` 无 diff，新增只在 `0057`；重启后积分、次数、编队、增幅、药水、结算台账、战绩不变。
3. **结算幂等**：同一 `bid` 的 `attack`/`lose` 重放与并发只结算一次（积分、药水、金币、对手扣分各一次）；`lc_used`/`rc_used` 重放只扣一次。
4. **门禁**：15 级以下、次数用尽、防御/攻击编队不满足、对手不存在时开战被拒且不扣次数。
5. **范围**：主角光环按钮保持置灰，`/main_chara_buff/*`、`/passive_skill/*` 保持空态；战斗掉落不含潜能叶子。
6. **仓库洁净**：不提交解密录播、APK、state、运行库。

## 2. 用例矩阵（M1001–M1012，模拟器 Poco）

| 用例 | 名称 | 客户端路径 | 端点 | UI / 网络 / SQLite 断言 |
|---|---|---|---|---|
| M1001 | 日常入口 | 主页 → 主人公的日常 | `/users/function_portal` | 列表含公会训练、癒术士大赛横幅；光环按钮仍置灰 |
| M1002 | 大赛主页 | 点癒术士大赛横幅 | `/ladders/show_ladder_info` | 显示 6 个按钮与规则；未设防御时点「开始挑战」弹提示并跳防御设定 |
| M1003 | 防御设定 | 防御设定 → 选魔宠 | `/ladders/monsters`、`/ladders/set_deck` | `cn35_ladder_decks` 第 1 格写入；长按移除后该格清空 |
| M1004 | 候选对手 | 开始挑战 | `/ladders/get_candidates` | 列表 20 行，含积分/等级/公会三行信息 |
| M1005 | 大赛战斗胜利 | 选对手 → 普通 → 出击 → 通关 | `execute`、`lc_used`、`attack` | `lc` 3→2；结算屏显示积分；`cn35_ladder_profiles.score` 增加、药水增加 |
| M1006 | 次数用尽 | 连续出击 3 次后再出击 | `execute` | 弹「今日挑战次数已用完」，`day_used` 不超过 3 |
| M1007 | 魔宠增幅 | 魔宠增幅 → 体力 → 火系升级 1 级 | `monster_grow_index`、`monster_grow_upgrade` | 药水减少 50；`cn35_ladder_grow` 步数 1；重置后药水返还 |
| M1008 | 排行榜 | 排行榜 | `/ladders/show_rank` | 本人在榜；无奖励时领取按钮不可用 |
| M1009 | 公会训练主页与购券 | 点公会训练横幅 → 免费入场 | `show_rob_battles_info`、`buy_rob_ticket` | `rc` 为 5；`cn35_rob_profiles.tickets_bought=1` |
| M1010 | 攻守编队 | 防守页签 / 战斗准备 → 编队 | `/user_rob_decks/on` | 两套各 10 格写入 `cn35_rob_decks`；第 3 名治疗被客户端拒绝 |
| M1011 | 公会训练战斗 | 挑战 → 战斗准备 → 开始 → 自动战斗结束 | `battle_ready`、`execute`、`rc_used`、`attack`/`lose` | `rc` 减 1；胜利时积分 +N 且结算屏显示；败北积分不变 |
| M1012 | 同库重启一致性 | 重启 `mercd35` 与客户端 | 全部 M10 端点 | 积分、次数、编队、增幅、药水、台账、战绩重启前后一致 |

## 3. 执行命令

```sh
make check
cd server && go test ./internal/cn35game/ -run 'TestM10' -v && go test ./internal/cn35store/ -run 'TestPvP' -v
python3 -m unittest discover -s tools/cn350_poco -t . -p 'test_m10*.py' -v
python3 -m tools.cn350_poco.m10_pvp_acceptance check
# 实机（未执行）：先 inspect/calibrate 校准 m10_pvp_selectors.candidate.json，产出 M10 服务端门禁后再 run
```

## 4. 实机验收（2026-11-21，模拟器手动驱动 + Poco 读树）

环境：MuMu 192.168.1.44:16416（Android 12，x86_64 + houdini），模式 A（服务端 + APK，gsp `192.168.1.69:18091`、第 8 字段 `false`），客户端 DLL 未改；Poco 通过 decoded 树内既有 `MainActivity$PocoLoader` 加载私有 `files/` 下测试桥（so `7cc5b328…`、dll `5053f468…`）。服务端为同日构建的 `mercd35`，state 为 `accept-m5m8-20261119` 的一致性备份副本 `state/accept-m10-20261121`（账号 test001，Lv100）。证据（截图/树/请求日志）留在仓库外 `output/poco/m10_inspect/` 与 state 目录，不入库。本轮为手动驱动，候选选择器仍未校准写入 `selectors.*.local.json`，`implementation_ready` 保持 `false`。

| 用例 | 结果 | 证据要点 |
|---|---|---|
| M1001 | 通过 | function_portal 列表含「公会训练」「癒术士大赛」横幅；「真·男主光环」置灰 |
| M1002 | 通过 | 无防御魔宠点「开始挑战」弹「至少要设置一只防御魔宠才可以发起战斗哟」，OK 后进入防御设定 |
| M1003 | 通过 | `set_deck` 写入 `cn35_ladder_decks` 第 0 格；长按 →「是」→ `remove_from_deck`，该格清空 |
| M1004 | 通过 | `get_candidates` 列表显示积分/等级/所属公会三行 |
| M1005 | 通过 | `execute → lc_used → attack`，剩余次数 3→2；结算屏「积分 +46（当前 92）」，掉落药水与会话 `rewards_json` 一致 |
| M1006 | 通过 | 第 4 次出击弹「今日挑战次数已用完，请明天再来」，`day_used=3` |
| M1007 | 通过 | 药水 5 个时升级弹窗数量保持 0；测试副本把体力药水置 120 后升级 1 级 → 70、`cn35_ladder_grow` 步数 1、火系体力 +20%；重置 → 120、grow 清空（之后药水改回 5） |
| M1008 | 通过 | `show_rank` 本人第 1（积分 138），无领取按钮 |
| M1009 | 通过 | 免费入场 → `rc_left=5`、`tickets_bought=1`；次日重置后再次免费入场 |
| M1010 | 通过 | 防守/攻击两套各 10 格写入 `cn35_rob_decks`；第 3 名治疗被客户端拒绝「治疗职业的同伴不可以超过2个」，无请求 |
| M1011 | 部分 | 攻击队不满 10 人被拒；`battle_ready`（敌方 10 名、胜利可获得 41 积分）→ `rc_used`（rc 5→4）→ 自动战斗败北 → `lose`，积分不变。**胜利分支未在实机触发**（测试账号可用于攻击队的高等级同伴仅 4 名），由 `TestM10RobFlow` 覆盖 |
| M1012 | 通过 | 重启 `mercd35` 与客户端：M10 九张表关键列前后逐行一致；大赛主页积分/评级/次数/胜率显示一致 |

实机中发现并已修复的问题（均补了回归测试）：

1. 大赛出击「加载失败」：`asset_names` 重复 `common/flag`，客户端 `Quest.OnPrepare` 对 `textures.Add` 重复键抛异常。`pvpAssetNames` 全段去重。
2. `/user_rob_decks/on` 使服务端整体挂死：`RobSetDeck` 事务内回调触达冷态 `rosterCatalog`，回源同一 SQLite 连接死锁。改为事务外预解析职业。
3. 大赛主页前三名在真实上榜不足 3 人时露出 prefab 占位（白方块 +「加入公会即可利用」）：以候选同源确定性 NPC 补足（重建值）。
