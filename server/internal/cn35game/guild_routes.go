// guild_routes.go 公会/会战域路由装配入口（GUILDCLOSE350 拆分）。
//
// 只做分发，不承载业务：拆成读面、成员/社交面、会战面、蓄水库/设施面
// 四个注册函数，分别由各自文件维护，避免多人并发改同一处注册表。
// 注册约定：GET 自动双注册 POST（客户端 requireData 以 POST 加密体访问读面）。
package cn35game

import (
	"net/http"

	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// guildRegister 公会域注册器：method/path/note/handler。
type guildRegister func(method, path, note string, handler httpapi.Handler)

// registerGuildRoutes 装配 dep.Guild 时注册；未装配走 requireData 空态。
func registerGuildRoutes(register func(method, path, note string, handler httpapi.Handler), dep Deps) {
	reg := guildRegister(func(method, path, note string, h httpapi.Handler) {
		register(method, path, note, h)
		if method == http.MethodGet {
			// 与既有读面同约定（version_info/presents/infos 等）：客户端
			// requireData 以 POST 加密体访问读面，双注册 GET+POST。
			register(http.MethodPost, path, note+" POST 形态", h)
		}
	})
	// 顺序即优先级：后注册同名路径会被 Registry 丢弃，因此把更细的
	// 社交/成员面放在通用读面之前（/waiting_users 由成员面 owns）。
	registerGuildMemberRoutes(reg, dep)
	registerGuildReadRoutes(reg, dep)
	registerGuildOrgWriteRoutes(reg, dep)
	registerGuildBattleRoutes(reg, dep)
	registerGuildStorageRoutes(reg, dep)
}
