// Package cn35store 提供 CN 3.5.0 账号/玩家/会话的 SQLite 持久实现。
//
// 归属（CN350-09）：实现 cn35game.AccountStore 与 cn35wire.SessionStore，
// 替换 mercd35 的 MemoryAccounts/MemoryStore 桩。表集 cn35_*（migration
// 0003）与 cn15 域完全隔离；单连接池 + 短事务沿用 database.Open 约定。
//
// 种子值证据（不再沿用 1.5 触发器数值）：
//   - level/exp/max_exp/ap/max_ap/bp/max_bp/gacha_point 取自客户端内嵌
//     Config/quest0 教程上下文 user（run/c-assets-05/extracted/quest0.json）；
//   - leader_unit_id=348 取 05 审计约束（unit_large_ns_* 仅 348/375 内嵌，
//     Home 队长位必须可本地渲染）；
//   - coin=0：无原始证据，取保守零值，首充/发放归后续任务。
package cn35store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/cn35game"
	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/database"
)

// CN350 初始玩家种子（证据见包注释）。
const (
	seedLeaderUnitID = "348"
	seedLevel        = 1
	seedExp          = 50
	seedMaxExp       = 100
	seedAP           = 20
	seedMaxAP        = 20
	seedBP           = 300
	seedMaxBP        = 300
	seedGachaPoint   = 302
	seedCoin         = 0
	seedUnitLevel    = 1
	// 建号种子队长发放台账键（幂等语义见 cn35_inventory_grants 主键）。
)

// 新手礼物箱预置（STARTER-GIFT）：建号同事务写入 cn35_user_presents，
// 玩家自行在礼物箱领取（/user_presents/receive 同事务发奖+标记）。
// 走运营发放通道而非余额种子，建号种子的证据口径（0003 注释）保持不变。
// content_type 对齐领取端映射：coin=钻石、gacha_point=金币、medal=徽章
// （present_receive_wire.go presentRewardDoc 的既有口径）。
var starterPresents = []struct {
	contentType string
	contentID   int64
	amount      int64
}{
	{"coin", 1, 99999},          // 钻石 ×99999（真机测试口径，原 2000）
	{"medal", 1, 5000},          // 徽章 ×5000
	{"gacha_point", 1, 1000000}, // 金币 ×1000000
}

// Store 持有 CN3.5 运行库。实现 cn35game.AccountStore + cn35wire.SessionStore。
type Store struct {
	db    *sql.DB
	clock func() time.Time

	// sizeColOnce/hasSizeCol 缓存「0009 魔宠 Size 列是否在位」的探测结果
	// （只读打开旧库时需要退回旧列集，见 monsterSizeColumns）。
	sizeColOnce sync.Once
	hasSizeCol  bool

	// waitColOnce/hasWaitCol 缓存「0046 同伴 is_waiting 列是否在位」的探测结果
	// （只读打开旧库时需要退回旧列集，见 unitWaitingColumn）。
	waitColOnce sync.Once
	hasWaitCol  bool

	// inflight 写面在途去重表（dedupe.go）。
	inflightMu sync.Mutex
	inflight   map[string]*inflightCall

	// tplGen 模板写入代际号：本进程内 cn35_unit_templates 写事务成功提交后
	// 递增，rosterCatalog 据此重建进程内模板快照（roster.go）。
	tplGen atomic.Uint64
}

// Open 打开/升级 CN3.5 运行库；schema 版本不符按迁移器规则失败关闭。
func Open(path string) (*Store, error) {
	db, err := database.Open(path)
	if err != nil {
		return nil, err
	}
	if err = database.Migrate(context.Background(), db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, clock: time.Now}, nil
}

// Close 关闭底层连接。
func (s *Store) Close() error { return s.db.Close() }

// DB 暴露运行库句柄（事务贯穿与排障用）。
func (s *Store) DB() *sql.DB { return s.db }

// SetClock 注入时钟（测试与「-guild-clock-offset」验收手段共用；
// 生产默认 time.Now）。只影响依赖时钟的业务判定，不改写落库时间戳格式。
func (s *Store) SetClock(clock func() time.Time) { s.clock = clock }

func (s *Store) clockNowUTC() time.Time {
	clock := s.clock
	if clock == nil {
		clock = time.Now
	}
	return clock().UTC()
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])[:n]
}

func hashPwd(pwd string) string {
	sum := sha256.Sum256([]byte("cn35-pwd|" + pwd))
	return hex.EncodeToString(sum[:])
}

// isIDConflict 判断唯一冲突是否落在随机 id 列（he_account_id/user_id）——
// 只有这类冲突允许换 id 重试。modernc.org/sqlite 的唯一冲突错误消息
// 形如 "UNIQUE constraint failed: cn35_accounts.he_account_id" 或索引名。
func isIDConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") &&
		(strings.Contains(msg, "he_account_id") || strings.Contains(msg, "user_id"))
}

// ---- 账号域（cn35game.AccountStore） ----

type account struct {
	heAccountID  string
	userID       string
	token        string
	sk           string
	userName     string
	accountName  string
	passwordHash string
	sdkAccountID string
	deviceID     string
	isNewUser    bool
	tutorialDone bool
	lastServerID string
	createdAt    string
	updatedAt    string
}

const accountCols = `he_account_id,user_id,token,sk,user_name,account_name,password_hash,sdk_account_id,device_id,is_new_user,tutorial_finish,last_server_id,created_at,updated_at`

func scanAccount(row interface{ Scan(...any) error }) (*account, error) {
	var a account
	var isNew, tut int
	err := row.Scan(&a.heAccountID, &a.userID, &a.token, &a.sk, &a.userName,
		&a.accountName, &a.passwordHash, &a.sdkAccountID, &a.deviceID,
		&isNew, &tut, &a.lastServerID, &a.createdAt, &a.updatedAt)
	if err != nil {
		return nil, err
	}
	a.isNewUser = isNew == 1
	a.tutorialDone = tut == 1
	return &a, nil
}

func (s *Store) findBy(ctx context.Context, tx *sql.Tx, col, val string) (*account, error) {
	return scanAccount(tx.QueryRowContext(ctx,
		`SELECT `+accountCols+` FROM cn35_accounts WHERE `+col+`=?`, val))
}

// findGuestByDevice 只匹配纯游客行（account_name/sdk_account_id 皆空），与
// cn35_accounts_device_key 的谓词一致——同机 HE 注册号/SDK 号共用 device_id
// 时，游客复登仍必须命中游客行。
func (s *Store) findGuestByDevice(ctx context.Context, tx *sql.Tx, deviceID string) (*account, error) {
	return scanAccount(tx.QueryRowContext(ctx,
		`SELECT `+accountCols+` FROM cn35_accounts
		 WHERE device_id=? AND account_name='' AND sdk_account_id=''`, deviceID))
}

// createAccount 在一个事务内建号+玩家种子初始化（原子：任一失败整体回滚）。
// id 采用随机 hex 撞库重试；返回 LoginResult{New:true}。
func (s *Store) createAccount(tx *sql.Tx, a *account) (cn35game.LoginResult, error) {
	ctx := context.Background()
	now := nowUTC()
	a.token = randHex(32)
	a.sk = randHex(24)
	a.isNewUser = true
	a.tutorialDone = false
	a.createdAt, a.updatedAt = now, now
	for i := 0; i < 8; i++ {
		a.heAccountID = "he" + randHex(16)
		a.userID = randHex(12)
		_, err := tx.ExecContext(ctx, `INSERT INTO cn35_accounts (`+accountCols+`)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			a.heAccountID, a.userID, a.token, a.sk, a.userName, a.accountName,
			a.passwordHash, a.sdkAccountID, a.deviceID, 1, 0,
			a.lastServerID, a.createdAt, a.updatedAt)
		if err == nil {
			break
		}
		if i == 7 {
			return cn35game.LoginResult{}, fmt.Errorf("cn35store: 建号冲突重试耗尽: %w", err)
		}
		// he_account_id/user_id 撞唯一 → 重抽；device/name/sdk 唯一冲突由调用方
		// 已先查重，走到这里说明并发竞争——直接失败由上层重入幂等返回。
		if !isIDConflict(err) {
			return cn35game.LoginResult{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cn35_player_profiles
		(user_id,level,exp,max_exp,ap,max_ap,ap_updated_at,bp,max_bp,leader_unit_id,login_bonus_receivable_sec,guild_name_snapshot,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,0,'',?,?)`,
		a.userID, seedLevel, seedExp, seedMaxExp, seedAP, seedMaxAP, now,
		seedBP, seedMaxBP, seedLeaderUnitID, now, now); err != nil {
		return cn35game.LoginResult{}, err
	}
	for _, cur := range []struct {
		name   string
		amount int64
	}{{"coin", seedCoin}, {"gacha_point", seedGachaPoint}, {"medal", 0}} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO cn35_player_balances
			(user_id,currency,amount,updated_at) VALUES (?,?,?,?)`,
			a.userID, cur.name, cur.amount, now); err != nil {
			return cn35game.LoginResult{}, err
		}
	}
	// STORY350-03：剧情进度种子与建号同事务（0006 初始值 1,1,1,0）。
	if err := insertQuestProgressSeedTx(ctx, tx, a.userID); err != nil {
		return cn35game.LoginResult{}, err
	}
	// STARTER-GIFT：新手礼物与建号同事务（失败整笔回滚不留半建号态）。
	if err := seedStarterPresentsTx(tx, a.userID, time.Now().Unix()); err != nil {
		return cn35game.LoginResult{}, err
	}
	return cn35game.LoginResult{HeAccountID: a.heAccountID, Token: a.token, New: true}, nil
}

// seedStarterPresentsTx 写新手礼物行；uuid 全局唯一（领取键）。
func seedStarterPresentsTx(tx *sql.Tx, userID string, nowUnix int64) error {
	for _, p := range starterPresents {
		if _, err := tx.Exec(`INSERT INTO cn35_user_presents
			(uuid,user_id,content_type,content_id,amount,expire_at,received_at,created_at)
			VALUES (?,?,?,?,?,0,0,?)`,
			"starter-"+randHex(16), userID, p.contentType, p.contentID, p.amount, nowUnix); err != nil {
			return fmt.Errorf("cn35store: 新手礼物 %s 写入失败: %w", p.contentType, err)
		}
	}
	return nil
}

// rotateToken 重发 token（每次登录幂等轮换，旧 token 立即失效）。
func rotateToken(tx *sql.Tx, a *account) error {
	a.token = randHex(32)
	a.updatedAt = nowUTC()
	_, err := tx.ExecContext(context.Background(),
		`UPDATE cn35_accounts SET token=?,updated_at=? WHERE he_account_id=?`,
		a.token, a.updatedAt, a.heAccountID)
	return err
}

func (s *Store) withTx(fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// QuickLogin 游客建号/复登：(device_id, 纯游客行) 持久幂等键——同机注册号/
// SDK 号不参与游客解析（迁移 0004 起索引同谓词收窄）。
func (s *Store) QuickLogin(deviceID string) (cn35game.LoginResult, error) {
	var res cn35game.LoginResult
	err := s.withTx(func(tx *sql.Tx) error {
		ctx := context.Background()
		if deviceID != "" {
			if a, err := s.findGuestByDevice(ctx, tx, deviceID); err == nil {
				if err = rotateToken(tx, a); err != nil {
					return err
				}
				res = cn35game.LoginResult{HeAccountID: a.heAccountID, Token: a.token}
				return nil
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		var err error
		res, err = s.createAccount(tx, &account{deviceID: deviceID})
		return err
	})
	return res, err
}

// Login HE 账密复登：account_name + password_hash 校验，device 绑定首登设备。
func (s *Store) Login(userName, pwd, deviceID string) (cn35game.LoginResult, error) {
	var res cn35game.LoginResult
	err := s.withTx(func(tx *sql.Tx) error {
		ctx := context.Background()
		a, err := s.findBy(ctx, tx, "account_name", userName)
		if errors.Is(err, sql.ErrNoRows) {
			return cn35game.ErrNoAccount
		}
		if err != nil {
			return err
		}
		if a.passwordHash == "" || a.passwordHash != hashPwd(pwd) {
			return cn35game.ErrBadCredential
		}
		if a.deviceID == "" && deviceID != "" {
			if _, err = tx.ExecContext(ctx,
				`UPDATE cn35_accounts SET device_id=? WHERE he_account_id=?`,
				deviceID, a.heAccountID); err != nil {
				return err
			}
		}
		if err = rotateToken(tx, a); err != nil {
			return err
		}
		res = cn35game.LoginResult{HeAccountID: a.heAccountID, Token: a.token}
		return nil
	})
	return res, err
}

// Register HE 注册：account_name 唯一（部分唯一索引兜底并发）。
func (s *Store) Register(accountName, pwd, deviceID, pf, bindEmail string) (cn35game.LoginResult, error) {
	if accountName == "" {
		return cn35game.LoginResult{}, fmt.Errorf("%w: accountName 为空", cn35game.ErrBadCredential)
	}
	var res cn35game.LoginResult
	err := s.withTx(func(tx *sql.Tx) error {
		ctx := context.Background()
		if _, err := s.findBy(ctx, tx, "account_name", accountName); err == nil {
			return cn35game.ErrNameTaken
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var err error
		res, err = s.createAccount(tx, &account{
			deviceID: deviceID, accountName: accountName, passwordHash: hashPwd(pwd),
		})
		if err != nil && strings.Contains(err.Error(), "account_name") {
			return cn35game.ErrNameTaken // 并发撞唯一索引（先查重未命中）
		}
		return err
	})
	return res, err
}

// AccountBind heAccountBind 统一入口（客户端绑定/转正/换机继承三面板共用）。
// 单事务内先判继承后判转正——新设备游客凭据恒合法，若先判转正，"名字已占用"
// 会让 TakeOver 继承永远不可达：
//   - account_name 已存在 → 账密匹配即接管该账号（返回其身份），错密 → ErrBadCredential；
//   - account_name 未占用 → (he_account_id,token) 校验后原行 UPDATE 写账密
//     （转正保留 user_id/进度），已绑其他名 → ErrAlreadyBound，并发抢名 → ErrNameTaken。
//
// 两分支均轮换 token；转正后行不再满足 findGuestByDevice 谓词，同设备再次
// QuickLogin 产出新游客（不撞 0004 收窄后的设备索引）。
// bindEmail/pf 协议接受但当前不落库（无对应列；resetPassword 实现时再评估）。
func (s *Store) AccountBind(heAccountID, token, accountName, pwd, deviceID, pf, bindEmail string) (cn35game.LoginResult, error) {
	if accountName == "" {
		return cn35game.LoginResult{}, fmt.Errorf("%w: accountName 为空", cn35game.ErrBadCredential)
	}
	var res cn35game.LoginResult
	err := s.withTx(func(tx *sql.Tx) error {
		ctx := context.Background()
		if a, err := s.findBy(ctx, tx, "account_name", accountName); err == nil {
			if a.passwordHash == "" || a.passwordHash != hashPwd(pwd) {
				return cn35game.ErrBadCredential
			}
			if a.deviceID == "" && deviceID != "" {
				if _, err = tx.ExecContext(ctx,
					`UPDATE cn35_accounts SET device_id=? WHERE he_account_id=?`,
					deviceID, a.heAccountID); err != nil {
					return err
				}
			}
			if err = rotateToken(tx, a); err != nil {
				return err
			}
			res = cn35game.LoginResult{HeAccountID: a.heAccountID, Token: a.token}
			return nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		a, err := s.findBy(ctx, tx, "he_account_id", heAccountID)
		if errors.Is(err, sql.ErrNoRows) {
			return cn35game.ErrNoAccount
		}
		if err != nil {
			return err
		}
		if token == "" || a.token != token {
			return cn35game.ErrBadCredential
		}
		if a.accountName != "" {
			return cn35game.ErrAlreadyBound
		}
		if a.deviceID == "" && deviceID != "" {
			a.deviceID = deviceID
		}
		// WHERE 谓词 account_name='' 兜底并发：若目标行在读写之间被并发转正，
		// RowsAffected=0 → ErrAlreadyBound，不覆盖他人账号名。
		if r, err := tx.ExecContext(ctx,
			`UPDATE cn35_accounts SET account_name=?,password_hash=?,device_id=?,updated_at=?
			 WHERE he_account_id=? AND account_name=''`,
			accountName, hashPwd(pwd), a.deviceID, nowUTC(), a.heAccountID); err != nil {
			if strings.Contains(err.Error(), "account_name") {
				return cn35game.ErrNameTaken
			}
			return err
		} else if n, _ := r.RowsAffected(); n == 0 {
			return cn35game.ErrAlreadyBound
		}
		if err = rotateToken(tx, a); err != nil {
			return err
		}
		res = cn35game.LoginResult{HeAccountID: a.heAccountID, Token: a.token}
		return nil
	})
	return res, err
}

// SDKLogin UC 通道：sdk_account_id 为幂等键，accessToken 静态接受（本地不验 UC）。
func (s *Store) SDKLogin(accountID, accessToken, loginExtend, deviceID string) (cn35game.LoginResult, error) {
	if accountID == "" {
		return cn35game.LoginResult{}, fmt.Errorf("%w: accountId 为空", cn35game.ErrBadCredential)
	}
	var res cn35game.LoginResult
	err := s.withTx(func(tx *sql.Tx) error {
		ctx := context.Background()
		if a, err := s.findBy(ctx, tx, "sdk_account_id", accountID); err == nil {
			if err = rotateToken(tx, a); err != nil {
				return err
			}
			res = cn35game.LoginResult{HeAccountID: a.heAccountID, Token: a.token}
			return nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var err error
		res, err = s.createAccount(tx, &account{deviceID: deviceID, sdkAccountID: accountID})
		return err
	})
	return res, err
}

// AccountCount 返回 cn35_accounts 账号总数（mercd35 /mercd/health 运维端点
// 只读统计用）。
func (s *Store) AccountCount() (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM cn35_accounts`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// NameAvailable heRegCheck：HE 登录名查重。
func (s *Store) NameAvailable(accountName string) bool {
	if accountName == "" {
		return false
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM cn35_accounts WHERE account_name=?`,
		accountName).Scan(&n); err != nil {
		return false
	}
	return n == 0
}

// NicknameAvailable verify_nickname（契约 13）：他人占用 → false；
// 无人占用或本账号已持有同名（断线重试）→ true。
func (s *Store) NicknameAvailable(userID, name string) bool {
	if name == "" {
		return false
	}
	var owner string
	err := s.db.QueryRow(`SELECT user_id FROM cn35_accounts WHERE user_name=?`,
		name).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return true
	}
	return err == nil && owner == userID
}

// UsersUpdate users/update（契约 12，11 接线）：昵称与教程完成在
// 同一事务落地——End 步一次请求同时提交 user_name+tutorial_finish，
// 昵称被他人占用时整笔回滚不留半写；tutorial_finish 重复置位幂等成功。
func (s *Store) UsersUpdate(userID, name string, tutorialFinish bool) error {
	return s.withTx(func(tx *sql.Tx) error {
		ctx := context.Background()
		if name != "" {
			var owner string
			err := tx.QueryRowContext(ctx,
				`SELECT user_id FROM cn35_accounts WHERE user_name=?`, name).Scan(&owner)
			if err == nil && owner != userID {
				return cn35game.ErrNameTaken
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		var res sql.Result
		var err error
		switch {
		case name != "" && tutorialFinish:
			res, err = tx.ExecContext(ctx,
				`UPDATE cn35_accounts SET user_name=?,tutorial_finish=1,updated_at=? WHERE user_id=?`,
				name, nowUTC(), userID)
		case name != "":
			res, err = tx.ExecContext(ctx,
				`UPDATE cn35_accounts SET user_name=?,updated_at=? WHERE user_id=?`,
				name, nowUTC(), userID)
		case tutorialFinish:
			res, err = tx.ExecContext(ctx,
				`UPDATE cn35_accounts SET tutorial_finish=1,updated_at=? WHERE user_id=?`,
				nowUTC(), userID)
		default:
			res, err = tx.ExecContext(ctx,
				`UPDATE cn35_accounts SET updated_at=? WHERE user_id=?`, nowUTC(), userID)
		}
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return cn35game.ErrNoAccount
		}
		return nil
	})
}

// UpdateMemo users/update_memo（0044）：单事务更新 cn35_player_profiles.memo。
// 空串 memo 保持原服 200 幂等语义（不覆盖既有非空签名）。
func (s *Store) UpdateMemo(userID, memo string) error {
	return s.withTx(func(tx *sql.Tx) error {
		ctx := context.Background()
		var res sql.Result
		var err error
		if memo != "" {
			res, err = tx.ExecContext(ctx,
				`UPDATE cn35_player_profiles SET memo=?,updated_at=? WHERE user_id=?`,
				memo, nowUTC(), userID)
		} else {
			res, err = tx.ExecContext(ctx,
				`UPDATE cn35_player_profiles SET updated_at=? WHERE user_id=?`,
				nowUTC(), userID)
		}
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return cn35game.ErrNoAccount
		}
		return nil
	})
}

// LastServer getServerList.lastLoginServer 来源（chooseServer 持久化 serverId）。
func (s *Store) LastServer(heAccountID string) string {
	var v string
	if err := s.db.QueryRow(`SELECT last_server_id FROM cn35_accounts WHERE he_account_id=?`,
		heAccountID).Scan(&v); err != nil {
		return ""
	}
	return v
}

// ChooseServer (heAccountId,token) → (userId,sk)；同事务持久化 last_server_id。
func (s *Store) ChooseServer(heAccountID, token, serverID string) (string, string, error) {
	var userID, sk string
	err := s.withTx(func(tx *sql.Tx) error {
		a, err := s.findBy(context.Background(), tx, "he_account_id", heAccountID)
		if errors.Is(err, sql.ErrNoRows) {
			return cn35game.ErrNoAccount
		}
		if err != nil {
			return err
		}
		if token == "" || a.token != token {
			return cn35game.ErrBadCredential
		}
		if serverID != "" {
			if _, err = tx.ExecContext(context.Background(),
				`UPDATE cn35_accounts SET last_server_id=?,updated_at=? WHERE he_account_id=?`,
				serverID, nowUTC(), heAccountID); err != nil {
				return err
			}
		}
		userID, sk = a.userID, a.sk
		return nil
	})
	return userID, sk, err
}

// Snapshot preset_data 的 data.user/user_coin/user_summary 来源。
// 缺 profile/必需余额行按不变量损坏返回错误，不回退共享玩家。
func (s *Store) Snapshot(userID string) (*cn35game.PlayerSnapshot, error) {
	// AP 补算先在独立短事务里提交；后续 Snapshot 继续走既有读路径，避免
	// 在 SQLite 单连接池事务内嵌套 quest/flag 查询。
	if err := s.withTx(func(tx *sql.Tx) error {
		_, _, _, err := s.recoverAPTx(tx, userID, s.clockNowUTC())
		return err
	}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, cn35game.ErrNoAccount
		}
		return nil, err
	}
	var a account
	var lvl, exp, maxExp, ap, maxAP, bp, maxBP, lbSec int64
	var leader, helper, guild, memo, apAt, createdAt string
	var tut int
	row := s.db.QueryRow(`SELECT a.user_name, a.tutorial_finish,
		p.level,p.exp,p.max_exp,p.ap,p.max_ap,p.ap_updated_at,p.bp,p.max_bp,
		p.leader_unit_id,p.helper_unit_id,p.login_bonus_receivable_sec,p.guild_name_snapshot,
		p.memo,p.created_at
		FROM cn35_accounts a JOIN cn35_player_profiles p ON p.user_id=a.user_id
		WHERE a.user_id=?`, userID)
	err := row.Scan(&a.userName, &tut, &lvl, &exp, &maxExp, &ap, &maxAP,
		&apAt, &bp, &maxBP, &leader, &helper, &lbSec, &guild, &memo, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, cn35game.ErrNoAccount
	}
	if err != nil {
		return nil, err
	}
	apUpdatedAt, err := time.Parse(time.RFC3339Nano, apAt)
	if err != nil {
		return nil, fmt.Errorf("cn35store: 无效 AP 时间锚点 user=%s: %w", userID, err)
	}
	// LOGIN350-02：泉水可领时刻由「上次领取锚点（0 值回落到建号时间）+3600s」
	// 派生，不再直接下发档案列（该列 0035 起只承担锚点存储）。
	createdTime, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return nil, fmt.Errorf("cn35store: 无效建号时间锚点 user=%s: %w", userID, err)
	}
	springAnchor := cn35game.SpringAnchorSec(lbSec, createdTime.Unix(), s.clockNowUTC().Unix())
	springReceivable := springAnchor + cn35game.SpringIntervalSec
	balances := map[string]int64{}
	rows, err := s.db.Query(`SELECT currency,amount FROM cn35_player_balances WHERE user_id=?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cur string
		var amt int64
		if err = rows.Scan(&cur, &amt); err != nil {
			return nil, err
		}
		balances[cur] = amt
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	name := a.userName
	if name == "" {
		name = "旅人"
	}
	// quest_id=1：quest0 内嵌教程上下文 user 的实测值（c-assets-05/quest0.json）。
	// 客户端 IsTutorialUser()=level<=7&&quest_id<=4、Home 首显引导覆盖层需
	// quest_id<=1（Common.cs:1577-1582）；缺省会让 quest_id 落到默认 5，
	// ShowGuideToQuestPage/ShowGuideToFirstBattle 被跳过。
	// STORY350-03 起进度指针从 cn35_quest_progress 读（stage_id/area_id/
	// quest_id/hard_mode_stage_id），无行→初始值；推进写归 STORY350-07。
	user := map[string]any{
		"id":      userID,
		"name":    name,
		"level":   lvl,
		"exp":     exp,
		"max_exp": maxExp,
		"ap":      ap,
		// max_ap 由等级推导（level+19），列值仅为存储态；0024 起升级事务
		// 同步刷新列值，此处以推导值下发保证口径唯一。
		"max_ap":            int64(cn35game.UserMaxAP(int(lvl))),
		"int_ap_updated_at": apUpdatedAt.Unix(),
		"current_ap_diff":   0,
		"bp":                bp,
		"max_bp":            maxBP,
		"leader_unit_id":    leader,
		"gacha_point":       balances["gacha_point"],
		// ITEM350-07：徽章余额同源下发（交易所读 data.user.medal）。
		"medal":                      balances["medal"],
		"login_bonus_receivable_sec": springReceivable,
		"guild_name":                 guild,
		// 结算条/等级表数据源（SWFQuestClear.cs:709-714 读 user.exp_table/
		// current_exp/level）。need_exp/exp_table 按用户等级经验表由当前等级
		// 推导（1 级输出 [100,100]，与生产抓包 serve_quest_execute_1.json 一致；
		// 曲线来源与证据见 cn35game/user_exp_table.go、2026-10-01 Note，
		// 取代 exp_table=[100,100] 两档样本口径）。
		"current_exp": exp,
		"need_exp":    cn35game.UserNeedExpCapped(int(lvl)),
		"exp_table":   cn35game.UserExpWindow(int(lvl)),
	}
	// ITEM350-02：培养素材库存投影到 user.powerup_items
	// （暂定契约：{<content_id>:<数量>}，待 ITEM350-01 核字段形状）。
	powerupItems := map[string]int64{}
	piRows, err := s.db.Query(`SELECT c.content_id,p.quantity
		FROM cn35_player_items p JOIN cn35_item_catalog c ON c.item_key=p.item_key
		WHERE p.user_id=? AND p.quantity>0 AND c.kind='material' AND c.enabled=1`,
		userID)
	if err != nil {
		return nil, err
	}
	for piRows.Next() {
		var cid int
		var qty int64
		if err = piRows.Scan(&cid, &qty); err != nil {
			piRows.Close()
			return nil, err
		}
		powerupItems[strconv.Itoa(cid)] = qty
	}
	if err = piRows.Close(); err != nil {
		return nil, err
	}
	user["powerup_items"] = powerupItems
	progFields, err := s.questProgressUserFields(userID)
	if err != nil {
		return nil, err
	}
	for k, v := range progFields {
		user[k] = v
	}
	// first_flag.failure → 客户端 hasQuestFailedOnce（DataCache.cs:229，
	// doc 非空且 failure!=0 即真）。0007 cn35_player_flags 落库；无行=0。
	var questFailed int64
	_ = s.db.QueryRow(`SELECT quest_failed FROM cn35_player_flags WHERE user_id=?`,
		userID).Scan(&questFailed)
	user["first_flag"] = map[string]any{"failure": questFailed}
	// 新手任务（TASK350 修复）：doing 列表驱动 Home 的 TutorialTaskIconParts
	// 入口；全部领完后 TutorialTaskDoingListDoc 返回 nil → 键省略、图标隐藏。
	if tprog, tclaimed, terr := s.TutorialTaskProgress(userID); terr == nil {
		if doing := cn35game.TutorialTaskDoingListDoc(tprog, tclaimed); doing != nil {
			user["tutorial_task_doing_list"] = doing
		}
	} else {
		return nil, terr
	}
	// 0016 helper_unit_id：未设置助战（''）时不下发该键；
	// 设置后以 TEXT 原值下发，wire 层 numericFieldsToNumber 转 NUMBER。
	if helper != "" {
		user["helper_unit_id"] = helper
	}
	// GUILD350-02：公会归属投影。未入会时 guild_id 下发空串（客户端
	// `s("guild_id","")==""` 判定未入会，UserPage.cs:248/265）；
	// 入会以实时成员表为准，并同步刷新 guild_name_snapshot。
	var ggid string
	var grole int
	if err := s.db.QueryRow(`SELECT guild_id,role FROM cn35_guild_members
		WHERE user_id=?`, userID).Scan(&ggid, &grole); err == nil && ggid != "" {
		user["guild_id"] = ggid
		user["guild_role"] = grole
		var gname string
		if err := s.db.QueryRow(`SELECT name FROM cn35_guilds WHERE guild_id=?`,
			ggid).Scan(&gname); err == nil && gname != "" {
			user["guild_name"] = gname
		}
	} else {
		user["guild_id"] = ""
		user["guild_role"] = 0
	}
	// 公会资材余额（GuildAssetModal 读 user.guild_asset_count）。
	user["guild_asset_count"] = balances["guild_asset"]
	if a.userName != "" {
		user["user_name"] = a.userName
	}
	if memo != "" {
		user["memo"] = memo
	}
	if tut == 1 {
		user["game_status"] = 2
	}
	cn35game.ApplyDefaultUserWireFields(user, balances)
	userCoin := map[string]any{"amount": balances["coin"]}
	cn35game.ApplyDefaultUserCoinWireFields(userCoin)
	return &cn35game.PlayerSnapshot{
		User:        user,
		UserCoin:    userCoin,
		UserSummary: map[string]any{},
	}, nil
}

// ---- 会话域（cn35wire.SessionStore） ----

// Load 按 userId 取持久会话。
func (s *Store) Load(userID string) (cn35wire.Session, error) {
	var sess cn35wire.Session
	err := s.db.QueryRow(`SELECT user_id,sk,s0,s1 FROM cn35_sessions WHERE user_id=?`,
		userID).Scan(&sess.UserID, &sess.SK, &sess.S0, &sess.S1)
	if errors.Is(err, sql.ErrNoRows) {
		return cn35wire.Session{}, cn35wire.ErrNoSession
	}
	return sess, err
}

// Save 绑定/覆盖会话（chooseServer 重进轮换 S0/S1 合法覆盖）。
func (s *Store) Save(sess cn35wire.Session) error {
	_, err := s.db.Exec(`INSERT INTO cn35_sessions (user_id,sk,s0,s1,updated_at)
		VALUES (?,?,?,?,?)
		ON CONFLICT(user_id) DO UPDATE SET sk=excluded.sk,s0=excluded.s0,
		s1=excluded.s1,updated_at=excluded.updated_at`,
		sess.UserID, sess.SK, sess.S0, sess.S1, nowUTC())
	return err
}
