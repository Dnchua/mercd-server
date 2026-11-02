// accounts.go CN 3.5.0 账号/会话的领域接口与进程内实现。
//
// 分层约定（CN350-07 交付，09 接管持久化）：
//
//	AccountStore —— verify 通道身份域：登录方法组、选服、玩家快照。
//	cn35wire.SessionStore —— 协议层会话域：userId → (S0,S1) 加密密钥对。
//
// chooseServer 成功路径由 handler 同时写两侧：AccountStore.ChooseServer 产出
// (userId,sk)，handler 生成 S0 并 SessionStore.Save{UserID,SK,S0,S1}。
//
// MemoryAccounts 是**受控开发桩**：进程内 map、确定性派生 id、不落盘。
// 它让启动链在无 09 持久化时可联调/测试；重启即丢账号属预期，生产部署
// 必须等 CN350-09 的 SQLite 实现接入，不得把桩当共享玩家库长期服役。
package cn35game

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// LoginResult 登录方法组统一回执（ucLogin/heQuickLogin/heLogin/heRegister）。
type LoginResult struct {
	HeAccountID string
	Token       string
	// New 标记新建账号 → 服务端在 events[] 下发 firstActive（客户端仅统计）。
	New bool
}

// PlayerSnapshot 是 preset_data 动态注入所需的最小玩家视图。
// 字段即 wire 文档本身，由实现方组装（09 起由 player_profiles/balances 映射）。
type PlayerSnapshot struct {
	User        map[string]any // data.user（id/name/level/... 见契约 11）
	UserCoin    map[string]any // data.user_coin
	UserSummary map[string]any // data.user_summary（可空）
}

// ErrNoAccount / ErrBadCredential 是受控错误：handler 映射为
// success:false 业务回执，不走 HTTP 错误。
var (
	ErrNoAccount     = errors.New("cn35game: 账号不存在")
	ErrBadCredential = errors.New("cn35game: 凭据校验失败")
	ErrNameTaken     = errors.New("cn35game: 账号名已占用")
	// ErrAlreadyBound 转正目标账号已绑定其他 HE 账号名（heAccountBind）。
	ErrAlreadyBound = errors.New("cn35game: 该账号已完成绑定")
)

// AccountStore 账号域接口。09 用 SQLite 实现替换 MemoryAccounts；
// 方法签名即契约卡 02/03/04/11 的消费面。
type AccountStore interface {
	// QuickLogin 游客一键登录：deviceId 幂等（同设备重入返回同一账号）。
	QuickLogin(deviceID string) (LoginResult, error)
	// Login HE 账密复登。
	Login(userName, pwd, deviceID string) (LoginResult, error)
	// Register HE 注册（heRegCheck 查重后）。
	Register(accountName, pwd, deviceID, pf, bindEmail string) (LoginResult, error)
	// AccountBind heAccountBind 统一入口（绑定/转正/换机继承共用）：
	// accountName 已存在且账密匹配 → 接管该账号（继承）；未占用且
	// (heAccountID,token) 命中未绑定账号 → 就地写账密转正，user_id/进度保留。
	// bindEmail/pf 当前协议接受但不消费。
	AccountBind(heAccountID, token, accountName, pwd, deviceID, pf, bindEmail string) (LoginResult, error)
	// SDKLogin UC SDK 路径（token 本地不校验，静态接受）。
	SDKLogin(accountID, accessToken, loginExtend, deviceID string) (LoginResult, error)
	// NameAvailable heRegCheck 查重。
	NameAvailable(accountName string) bool
	// ChooseServer 以 (heAccountId,token) 换 (userId,sk)。
	ChooseServer(heAccountID, token, serverID string) (userID, sk string, err error)
	// LastServer getServerList.lastLoginServer 来源；无记录返回 ""。
	LastServer(heAccountID string) string
	// NicknameAvailable verify_nickname（契约 13）游戏昵称查重；
	// userID 用于"本人已持有同名视为可用"的断线重试语义。
	NicknameAvailable(userID, name string) bool
	// UsersUpdate users/update 原子写（契约 12）：End 步 user_name 与
	// tutorial_finish 同事务落地，失败不留半写；tutorial_finish 重复置位幂等成功。
	UsersUpdate(userID, name string, tutorialFinish bool) error
	// UpdateMemo users/update_memo 玩家个人签名持久化（UserPage.cs:629-639）。
	UpdateMemo(userID, memo string) error
	// Snapshot 玩家快照（preset_data 的 data.user/user_coin 来源）。
	Snapshot(userID string) (*PlayerSnapshot, error)
}

// ---- MemoryAccounts：进程内受控桩 ----

type memAccount struct {
	heAccountID  string
	token        string
	userID       string
	sk           string
	userName     string // HE 登录名（account_name）
	nickname     string // 游戏昵称（user_name）
	memo         string // 玩家个人签名（memo）
	deviceID     string
	lastServerID string
	tutorialDone bool
}

// MemoryAccounts 不落盘的 AccountStore。id 派生确定：同 deviceId 重进
// QuickLogin 返回同一账号（进程内幂等语义可被测试断言）。
type MemoryAccounts struct {
	mu        sync.Mutex
	byHe      map[string]*memAccount
	byDevice  map[string]*memAccount
	byUserID  map[string]*memAccount
	byName    map[string]*memAccount
	byUserPwd map[string]string // userName -> pwd（明文桩，仅本机开发）
}

// NewMemoryAccounts 构造空账号表。
func NewMemoryAccounts() *MemoryAccounts {
	return &MemoryAccounts{
		byHe:      make(map[string]*memAccount),
		byDevice:  make(map[string]*memAccount),
		byUserID:  make(map[string]*memAccount),
		byName:    make(map[string]*memAccount),
		byUserPwd: make(map[string]string),
	}
}

func memID(prefix, seed string, n int) string {
	sum := sha256.Sum256([]byte("cn350-mem|" + seed))
	return prefix + hex.EncodeToString(sum[:])[:n]
}

func randToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (m *MemoryAccounts) insert(a *memAccount) LoginResult {
	m.byHe[a.heAccountID] = a
	m.byUserID[a.userID] = a
	// 设备键仅挂纯游客行（与 0004 收窄索引/FindGuestByDevice 谓词一致）——
	// 注册号/SDK 号不抢占设备映射，转正后也不再经此键解析。
	if a.deviceID != "" && a.userName == "" {
		m.byDevice[a.deviceID] = a
	}
	if a.userName != "" {
		m.byName[a.userName] = a
	}
	return LoginResult{HeAccountID: a.heAccountID, Token: a.token, New: true}
}

// QuickLogin 实现 AccountStore：deviceId 命中的老账号幂等返回。
func (m *MemoryAccounts) QuickLogin(deviceID string) (LoginResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if deviceID != "" {
		if a := m.byDevice[deviceID]; a != nil && a.userName == "" {
			a.token = randToken()
			return LoginResult{HeAccountID: a.heAccountID, Token: a.token}, nil
		}
	}
	a := &memAccount{
		heAccountID: memID("he", "quick|"+deviceID, 16),
		token:       randToken(),
		userID:      memID("", "uid|"+deviceID, 12),
		sk:          memID("sk", "sk|"+deviceID, 24),
		deviceID:    deviceID,
	}
	return m.insert(a), nil
}

// Login 实现 AccountStore：userName+pwd 校验（桩内明文比对）。
func (m *MemoryAccounts) Login(userName, pwd, deviceID string) (LoginResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.byName[userName]
	if a == nil {
		return LoginResult{}, ErrNoAccount
	}
	if want := m.byUserPwd[userName]; want != "" && want != pwd {
		return LoginResult{}, ErrBadCredential
	}
	a.token = randToken()
	if a.deviceID == "" {
		a.deviceID = deviceID // 已注册账号不占游客设备键
	}
	return LoginResult{HeAccountID: a.heAccountID, Token: a.token}, nil
}

// Register 实现 AccountStore。
func (m *MemoryAccounts) Register(accountName, pwd, deviceID, pf, bindEmail string) (LoginResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if accountName == "" {
		return LoginResult{}, fmt.Errorf("%w: accountName 为空", ErrBadCredential)
	}
	if m.byName[accountName] != nil {
		return LoginResult{}, ErrNameTaken
	}
	a := &memAccount{
		heAccountID: memID("he", "reg|"+accountName, 16),
		token:       randToken(),
		userID:      memID("", "uid|"+accountName, 12),
		sk:          memID("sk", "sk|"+accountName, 24),
		userName:    accountName,
		deviceID:    deviceID,
	}
	m.byUserPwd[accountName] = pwd
	return m.insert(a), nil
}

// AccountBind 实现 AccountStore：与 cn35store 同语义——已注册名账密匹配
// 即接管（继承），未占用名绑到 (heAccountId,token) 指定的未绑定账号（转正）。
func (m *MemoryAccounts) AccountBind(heAccountID, token, accountName, pwd, deviceID, pf, bindEmail string) (LoginResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if accountName == "" {
		return LoginResult{}, fmt.Errorf("%w: accountName 为空", ErrBadCredential)
	}
	if a := m.byName[accountName]; a != nil {
		if want := m.byUserPwd[accountName]; want != "" && want != pwd {
			return LoginResult{}, ErrBadCredential
		}
		a.token = randToken()
		if a.deviceID == "" && deviceID != "" {
			a.deviceID = deviceID // 已绑定账号不占游客设备键
		}
		return LoginResult{HeAccountID: a.heAccountID, Token: a.token}, nil
	}
	a := m.byHe[heAccountID]
	if a == nil {
		return LoginResult{}, ErrNoAccount
	}
	if token == "" || a.token != token {
		return LoginResult{}, ErrBadCredential
	}
	if a.userName != "" && !strings.HasPrefix(a.userName, "sdk|") {
		return LoginResult{}, ErrAlreadyBound
	}
	a.userName = accountName
	m.byName[accountName] = a
	m.byUserPwd[accountName] = pwd
	if a.deviceID == "" && deviceID != "" {
		a.deviceID = deviceID
	} else {
		delete(m.byDevice, a.deviceID) // 转正行不再是游客，让出设备键
	}
	a.token = randToken()
	return LoginResult{HeAccountID: a.heAccountID, Token: a.token}, nil
}

// SDKLogin 实现 AccountStore：以 accountId 为身份键幂等。
func (m *MemoryAccounts) SDKLogin(accountID, accessToken, loginExtend, deviceID string) (LoginResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := "sdk|" + accountID
	if a := m.byName[key]; a != nil {
		a.token = randToken()
		return LoginResult{HeAccountID: a.heAccountID, Token: a.token}, nil
	}
	a := &memAccount{
		heAccountID: memID("he", key, 16),
		token:       randToken(),
		userID:      memID("", "uid|"+key, 12),
		sk:          memID("sk", "sk|"+key, 24),
		userName:    key,
		deviceID:    deviceID,
	}
	res := m.insert(a)
	m.byUserPwd[key] = accessToken // 仅作存在性占位，本地不验 UC token
	return res, nil
}

// NameAvailable 实现 AccountStore。
func (m *MemoryAccounts) NameAvailable(accountName string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return accountName != "" && m.byName[accountName] == nil
}

// ChooseServer 实现 AccountStore：token 校验后返回 (userId,sk)。
func (m *MemoryAccounts) ChooseServer(heAccountID, token, serverID string) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.byHe[heAccountID]
	if a == nil {
		return "", "", ErrNoAccount
	}
	if token != "" && a.token != token {
		return "", "", ErrBadCredential
	}
	if serverID != "" {
		a.lastServerID = serverID
	}
	return a.userID, a.sk, nil
}

// LastServer 实现 AccountStore。
func (m *MemoryAccounts) LastServer(heAccountID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a := m.byHe[heAccountID]; a != nil {
		return a.lastServerID
	}
	return ""
}

// NicknameAvailable 实现 AccountStore：他人占用 → false；无人或本账号已持有 → true。
func (m *MemoryAccounts) NicknameAvailable(userID, name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if name == "" {
		return false
	}
	for uid, a := range m.byUserID {
		if a.nickname == name && uid != userID {
			return false
		}
	}
	return true
}

// UsersUpdate 实现 AccountStore：昵称+教程完成同锁原子写；
// 昵称被他人占用时整体失败不留半写。
func (m *MemoryAccounts) UsersUpdate(userID, name string, tutorialFinish bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.byUserID[userID]
	if a == nil {
		return ErrNoAccount
	}
	if name != "" {
		for uid, other := range m.byUserID {
			if uid != userID && other.nickname == name {
				return ErrNameTaken
			}
		}
		a.nickname = name
	}
	if tutorialFinish {
		a.tutorialDone = true
	}
	return nil
}

// UpdateMemo 实现 AccountStore：更新内存桩内玩家个人签名。
func (m *MemoryAccounts) UpdateMemo(userID, memo string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.byUserID[userID]
	if a == nil {
		return ErrNoAccount
	}
	if memo != "" {
		a.memo = memo
	}
	return nil
}

// cn350 初始玩家占位值（开发桩）。
//
//   - leader_unit_id=348：CN350-05 审计证实 unit_large_ns_* 仅 348/375 内嵌，
//     Home LeaderUnit 部件须二选一（resource-audit/handoffs/05.md）。
//   - level/exp/ap/bp：09 起以客户端内嵌 Config/quest0 教程上下文 user 定稿
//     （level1、exp50/max_exp100、max_ap20、max_bp300），与 cn35store 种子一致。
const (
	stubLeaderUnitID = "348"
	stubLevel        = 1
	stubExp          = 50
	stubMaxExp       = 100
	stubAP           = 20
	stubBP           = 300
	// stubQuestID quest0 内嵌教程上下文实测值；IsTutorialUser/Home 引导覆盖层
	// 门槛（level<=7 && quest_id<=4 / quest_id<=1），与 cn35store 常量下发一致。
	stubQuestID = 1
)

// Snapshot 实现 AccountStore：新账号骨架。name 占位 "旅人"（客户端昵称
// 权威字段 user_name 在教程命名后才下发——教程中不带 game_status=2）。
func (m *MemoryAccounts) Snapshot(userID string) (*PlayerSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.byUserID[userID]
	if a == nil {
		return nil, ErrNoAccount
	}
	name := a.nickname
	if name == "" || len(name) > 4 && name[:4] == "sdk|" {
		name = "旅人"
	}
	user := map[string]any{
		"id":                         a.userID,
		"name":                       name,
		"level":                      stubLevel,
		"exp":                        stubExp,
		"max_exp":                    stubMaxExp,
		"quest_id":                   stubQuestID,
		"ap":                         stubAP,
		"max_ap":                     stubAP,
		"bp":                         stubBP,
		"max_bp":                     stubBP,
		"leader_unit_id":             stubLeaderUnitID,
		"gacha_point":                0,
		"login_bonus_receivable_sec": 0,
		"guild_name":                 "",
		// STORY350-03：剧情进度字段（quest0 内嵌实证初始 stage/area/quest=1；
		// hard_mode 未开放=0）。内存桩恒为初始进度；SQLite 实现读
		// cn35_quest_progress。
		"stage_id":           1,
		"area_id":            1,
		"hard_mode_stage_id": 0,
		// 结算条/等级表数据源（quest0.json 采样，与 cn35store.Snapshot 同键；
		// 1 级窗口 [100,100] 与用户等级经验表一致，见 user_exp_table.go）。
		"current_exp": stubExp,
		"need_exp":    stubMaxExp,
		"exp_table":   UserExpWindow(stubLevel),
		"first_flag":  map[string]any{"failure": 0},
	}
	if a.nickname != "" {
		user["user_name"] = a.nickname
	}
	if a.memo != "" {
		user["memo"] = a.memo
	}
	if a.tutorialDone {
		user["game_status"] = 2
	}
	ApplyDefaultUserWireFields(user, nil)
	return &PlayerSnapshot{
		User:        user,
		UserCoin:    map[string]any{"amount": 0},
		UserSummary: map[string]any{},
	}, nil
}

// ApplyDefaultUserWireFields 补齐国服 3.5.0 data.user 协议基线字段（货币/代币、
// 道具库存哈希、经验分项、档案时间戳与主页环境挂件），已由存储层显式赋值的键保持不变。
func ApplyDefaultUserWireFields(user map[string]any, balances map[string]int64) {
	bal := func(k string) int64 {
		if balances == nil {
			return 0
		}
		return balances[k]
	}
	setDefault := func(k string, v any) {
		if _, exists := user[k]; !exists {
			user[k] = v
		}
	}
	// 1. 货币/代币类（11 项）
	setDefault("p_medal", bal("p_medal"))
	setDefault("friend_medal", bal("friend_medal"))
	setDefault("cp", int64(120))
	setDefault("max_cp", int64(120))
	setDefault("lc", int64(3))
	setDefault("max_lc", int64(3))
	setDefault("rc", int64(5))
	setDefault("max_rc", int64(5))
	setDefault("seal", bal("seal"))
	setDefault("petal", bal("petal"))
	setDefault("month_card_gacha_ticket", bal("month_card_gacha_ticket"))

	// 2. 道具库存哈希类（9 项）
	setDefault("chest_paper", map[string]any{"200": int64(0), "201": int64(0)})
	setDefault("consume_ring", map[string]any{"1": int64(0)})
	setDefault("elements", map[string]any{"1": int64(0), "2": int64(0), "3": int64(0), "4": int64(0), "5": int64(0)})
	setDefault("potions", map[string]any{"1": int64(0), "2": int64(0), "3": int64(0)})
	setDefault("scout_books", map[string]any{"1": int64(0), "99": int64(0), "100": int64(0), "101": int64(0)})
	setDefault("evolve_items", map[string]any{"1": int64(0)})
	setDefault("evolve_fancy_items", map[string]any{"102": int64(0), "203": int64(0), "301": int64(0), "502": int64(0), "503": int64(0)})
	setDefault("upgrade_items", map[string]any{"1": int64(0), "2": int64(0), "3": int64(0)})
	setDefault("token_money_hash", map[string]any{"1": int64(0), "2": int64(0), "3": int64(0)})
	if pi, ok := user["powerup_items"].(map[string]int64); ok {
		if len(pi) == 0 {
			pi["1"] = 0
		}
	} else if piAny, ok := user["powerup_items"].(map[string]any); ok {
		if len(piAny) == 0 {
			piAny["1"] = int64(0)
		}
	} else {
		setDefault("powerup_items", map[string]int64{"1": 0})
	}

	// 3. 经验分项类（3 项）
	setDefault("attack_exp", int64(0))
	setDefault("help_exp", int64(0))
	setDefault("pray_exp", int64(0))

	// 4. 档案、时间戳与主页展示字段
	setDefault("memo", "请多多指教！")
	setDefault("joined_at", "2017/10/26 20:06:51")
	setDefault("logged_at", "2017/11/10 12:00:00")
	setDefault("last_logged_at", "2017/11/10 11:00:00")
	setDefault("updated_at", "2017/11/10 12:00:00")
	setDefault("updated_at_i", int64(1510286400))
	setDefault("ap_updated_at", "2017/11/10 12:00:00")
	setDefault("bp_updated_at", "2017/11/10 12:00:00")
	setDefault("int_ap_updated_at", int64(1510286400))
	setDefault("max_ap_recovery_time", int64(0))
	setDefault("main_character_id", int64(100384))
	// BackgroundParts.cs:78-105：bg_id 非空时会拼 storybackground/storybackground<bg_id>
	// 并在 while(!IsRequestDone) 等待；默认置空以使用页面标准背景 common/bg-house_2。
	setDefault("bg_id", "")
	setDefault("bgm_id", "00001")
	setDefault("battle_id", "")
	setDefault("guild_id", "")
	setDefault("guild_joined_at", "2017/10/26 20:06:51")
	setDefault("guild_asset_count", bal("guild_asset"))
	setDefault("guild_asset_count_str", strconv.FormatInt(bal("guild_asset"), 10))
	setDefault("medal", bal("medal"))
	setDefault("medal_str", strconv.FormatInt(bal("medal"), 10))
	setDefault("hard_mode_area_id", int64(1))
	setDefault("hard_mode_quest_id", int64(1))
	setDefault("hard_mode_version", int64(1))
	setDefault("help_community", int64(1))
	setDefault("help_guild", int64(1))
	setDefault("help_id", "")
	setDefault("helped_at", "2017/11/10 12:00:00")
	setDefault("bind_on_registe", nil)
	setDefault("bind_temp_email_update_at", nil)
	setDefault("bind_update_at", int64(1509018011))
	setDefault("black_gacha_saved", int64(0))
	setDefault("black_gacha_times", int64(0))
	setDefault("cake", int64(0))
	setDefault("couplet", int64(0))
	setDefault("sakura_ticket", int64(0))
	setDefault("talisman", int64(0))
	setDefault("community_topic_id", int64(0))
	setDefault("consume_ring_1", int64(0))
	setDefault("force_rush", false)
	setDefault("force_rush_count", int64(0))
	setDefault("rush_count", int64(0))
	setDefault("rush_mode", "0")
	setDefault("rush_mode_ap", int64(0))
	setDefault("good_feel_item", map[string]any{})
	setDefault("good_feel_item2", map[string]any{"1": int64(0)})
	setDefault("invitor_id", nil)
	setDefault("last_login_time", nil)
	setDefault("lock_sell", nil)
	setDefault("modify_pass_update_at", int64(0))
	setDefault("payment_bonus_his", []any{})
	setDefault("payment_bonus_version", int64(1))
	setDefault("payment_discount_his", []any{})
	setDefault("payment_discount_version", int64(1))
	setDefault("quest_weather_valve", nil)
	setDefault("rest_activity", nil)
	setDefault("spring_stone", nil)
	setDefault("r_id", "0")
	setDefault("r_key", "")
	setDefault("real_name_at", "2017/10/26 20:06:51")
	setDefault("real_name_binded_phone", "")
	setDefault("real_name_poped", int64(1))
	setDefault("scout_book_1", int64(0))
	setDefault("scout_book_2", int64(0))
	setDefault("scout_book_3", int64(0))
	setDefault("scout_book_99", int64(0))
	setDefault("scout_book_100", int64(0))
	setDefault("scout_book_101", int64(0))
	setDefault("token_money_1", int64(0))
	setDefault("token_money_2", int64(0))
	setDefault("token_money_3", int64(0))
	setDefault("te_ended_at", int64(0))
	setDefault("te_level", int64(0))
	setDefault("share_monsters", map[string]any{
		"1": map[string]any{
			"monster_id": int64(1), "protected": false, "scale": 1.0, "scale_limit": 1.8, "unit_id": int64(0), "uuid": "",
		},
	})
	setDefault("tutorial_task_current_level", int64(1))
	setDefault("tutorial_task_done_list", []any{})
	setDefault("tutorial_task_finished_level", []any{int64(1)})
	setDefault("tutorial_task_version", int64(1))
	defaultTaskList := func() map[string]any {
		return map[string]any{
			"tasks":      []any{map[string]any{"id": int64(1), "count": int64(0), "fetched": false}},
			"updated_at": int64(1509018011),
		}
	}
	setDefault("anniversary_task_list", defaultTaskList())
	setDefault("anniversary2_task_list", defaultTaskList())
	setDefault("new_year_task_list", defaultTaskList())
	setDefault("school_festival_daily_tasks", defaultTaskList())
	setDefault("user_recall_tasks", map[string]any{"tasks": []any{}, "updated_at": int64(1509018011)})
	// WeatherParts.cs:32-38：effect_asset_name 为空串时走 ClearWeather() 静默清理，
	// 避免 Resources.Load 报 "Can not load weather"。
	setDefault("weather", map[string]any{"id": int64(1), "effect_asset_name": "", "offset_y": int64(0), "offset_z": int64(0)})
	// ToyPart.cs:203-246：id=1 对应内嵌 unit/unit_1，动画名使用 s/r（与 serve_app_conf_android.json 一致）。
	setDefault("toy", map[string]any{
		"id": int64(1), "drag": true, "scaleX": 1.0, "scaleY": 1.0,
		"collider_center_x": 0.0, "collider_center_y": 0.0, "collider_size_x": 100.0, "collider_size_y": 100.0,
		"ss_toy": map[string]any{
			"id": int64(1), "init_animation": "s",
			"ai": map[string]any{
				"animation": "r", "idle_animation": "s", "idle_time": 3.0, "init_on_off": true, "speed": 1.0,
				"bound_min_x": -200.0, "bound_max_x": 200.0, "bound_min_y": -100.0, "bound_max_y": 100.0,
			},
			"on_press":   map[string]any{"ai_on_off": false, "animation": "s"},
			"on_release": map[string]any{"ai_on_off": true, "animation": "s"},
		},
	})
}

// ApplyDefaultUserCoinWireFields 为 data.user_coin 补齐真实国服抓包中的完整字段。
func ApplyDefaultUserCoinWireFields(userCoin map[string]any) {
	if userCoin == nil {
		return
	}
	setDefault := func(k string, v any) {
		if _, exists := userCoin[k]; !exists {
			userCoin[k] = v
		}
	}
	setDefault("amount", int64(0))
	setDefault("paid_amount", int64(0))
	setDefault("monthly_amount", int64(0))
	setDefault("monthly_coins", int64(0))
	setDefault("id", int64(1))
	setDefault("last_purchased_yyyymm", "")
	setDefault("created_at", "2017/10/26 20:06:51")
	setDefault("updated_at", "2017/11/10 12:00:00")
	setDefault("expired_at", "2017/11/10 12:00:00")
}
