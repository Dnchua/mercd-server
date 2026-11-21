// Package apidoc 将已登记协议元数据接入 Huma，不扫描源码或自然语言说明。
// 旧 handler 通过 AddOperation 兼容；不改变游戏校验、匹配或响应编码。
package apidoc

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/Dnchua/mercd-server/server/internal/httpapi"
	"github.com/danielgtaylor/huma/v2"
)

type Options struct {
	Service, Title, Version, BaseURL, AssetBase string
	Now                                         time.Time
}

func (o Options) withDefaults() Options {
	if o.Service == "" {
		o.Service = "cn35"
	}
	if o.Title == "" {
		o.Title = "梅露可物语 国服3.5.0 本地复原服务 API"
	}
	if o.Version == "" {
		o.Version = "3.5.0-cn350"
	}
	if o.BaseURL == "" {
		o.BaseURL = "http://127.0.0.1:18091"
	}
	if o.AssetBase == "" {
		o.AssetBase = "../../assets/cn350"
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	return o
}

// Spec 以 Huma 标准类型和 Schema registry 兼容全部已登记路由。
// 未指定 ResponseModel 的旧 handler 只提供开放 Schema，不推测具体字段。
func Spec(reg *httpapi.Registry, opt Options) *huma.OpenAPI {
	opt = opt.withDefaults()
	schemas := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	spec := &huma.OpenAPI{
		OpenAPI: "3.1.0",
		Info: &huma.Info{Title: opt.Title, Version: opt.Version, Description: "CN3.5 当前登记接口；GET/POST 和尾斜杠别名分别列出。\n\n" +
			"旧协议通过 Huma AddOperation 兼容，尚未迁移为类型化 handler，不因此获得自动校验。\n" +
			"x-model-status 为既有模型状态，confirmed 不代表业务全部恢复。\n" +
			"/account 使用二进制帧；游戏业务支持 AES+zlib 加密，Swagger 不自动加密。\n" +
			"未结构化字段见实际处理器及 docs/协议对接/cn350/contracts/；不从说明文字推断参数。"},
		Servers:    []*huma.Server{{URL: opt.BaseURL}},
		Components: &huma.Components{Schemas: schemas, SecuritySchemes: map[string]*huma.SecurityScheme{}},
		Extensions: map[string]any{"x-generated-at": opt.Now.UTC().Format(time.RFC3339),
			"x-generator": "Huma v2（CN3.5 Registry 兼容接入）", "x-route-count": reg.Len(),
			"x-route-methods": "GET/POST 与尾斜杠别名分别登记；不是独立业务功能计数"},
	}
	tags := map[string]bool{}
	for _, route := range reg.Routes() {
		path, pathParam := docPath(route)
		detail := detailFor(route.Path)
		op := &huma.Operation{Method: route.Method, Path: path, OperationID: operationID(route),
			Tags: []string{route.Group}, Summary: summaryOf(route.Note), Description: route.Note,
			Extensions: map[string]any{"x-model-status": route.Status.String(), "x-route-prefix": route.Prefix, "x-schema-coverage": "transport-only"},
			Responses:  map[string]*huma.Response{},
		}
		if pathParam != nil {
			op.Parameters = append(op.Parameters, pathParam)
			op.Description += "\n\n最长前缀匹配，可含多级路径；{filename} 是文档模板，不代替服务器匹配规则。"
		}
		if detail.Doc != "" {
			op.Description += "\n\n契约/证据：" + detail.Doc
		}
		if !strings.HasPrefix(route.Path, httpapi.OpsPrefix) {
			op.Parameters = append(op.Parameters, &huma.Param{Name: "App_id_1", In: "header", Schema: &huma.Schema{Type: "string"}, Description: "可选的加密 userId（DES-ECB/PKCS5，hex），不是明文账号或 Bearer token"})
		}
		for _, p := range detail.Query {
			op.Parameters = append(op.Parameters, &huma.Param{Name: p.Name, In: "query", Description: p.Desc, Schema: &huma.Schema{Type: "string"}})
		}
		if route.Status == httpapi.ModelConfirmed {
			media := detail.ResponseContentType
			if media == "" {
				media = "application/json"
			}
			var schema *huma.Schema
			if route.ResponseModel != nil {
				schema = schemas.Schema(reflect.TypeOf(route.ResponseModel), true, "")
				op.Extensions["x-schema-coverage"] = "typed-response"
			} else if media == "application/json" {
				schema = &huma.Schema{Description: "字段尚未结构化；以实际契约为准"}
			} else {
				schema = &huma.Schema{Type: "string"}
				if media == "application/octet-stream" {
					schema.Format = "binary"
				}
			}
			mt := &huma.MediaType{Schema: schema}
			if detail.Fixture != "" && opt.AssetBase != "-" {
				mt.Examples = map[string]*huma.Example{"fixture": {Summary: detail.Fixture, ExternalValue: opt.AssetBase + "/" + detail.Fixture}}
				op.Extensions["x-fixture-file"] = detail.Fixture
			}
			content := map[string]*huma.MediaType{media: mt}
			if detail.Encrypted {
				content["application/octet-stream"] = &huma.MediaType{Schema: &huma.Schema{Type: "string", Format: "binary"}}
				op.Description += "\n\n响应可能为 AES+zlib 密文；Try it out 不负责游戏协议加解密。"
			}
			op.Responses["200"] = &huma.Response{Description: "已登记 handler 的响应；模型状态不代表全部业务恢复", Content: content}
		} else {
			op.Responses["501"] = diagnosticResponse(schemas, "模型未确认，返回脱敏诊断；不声明 200 成功")
		}
		op.Responses["500"] = diagnosticResponse(schemas, "handler 错误或 panic，由 httpapi 返回诊断")

		op.RequestBody = requestBody(route, detail)
		spec.AddOperation(op)
		tags[route.Group] = true
	}
	names := make([]string, 0, len(tags))
	for name := range tags {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		spec.Tags = append(spec.Tags, &huma.Tag{Name: name, Description: groupTitles[name]})
	}
	return spec
}

// webAuthGated 报告路径是否属于「配置 MERC35_WEB_AUTHKEY 后要求凭据」的
// 运维/文档面（未配置时无鉴权）。adminapi 恒受保护，不在此列。
func webAuthGated(path string) bool {
	switch path {
	case "/mercd/health", "/mercd/routes", "/mercd/unknown-endpoints", "/mercd/api":
		return true
	}
	return strings.HasPrefix(path, Prefix)
}

func diagnosticResponse(schemas huma.Registry, description string) *huma.Response {
	return &huma.Response{Description: description, Content: map[string]*huma.MediaType{
		"application/json": {Schema: schemas.Schema(reflect.TypeOf(httpapi.Diagnostic{}), true, "Diagnostic")},
	}}
}

func docPath(route httpapi.Route) (string, *huma.Param) {
	if !route.Prefix {
		return route.Path, nil
	}
	return strings.TrimSuffix(route.Path, "/") + "/{filename}", &huma.Param{Name: "filename", In: "path", Required: true, Schema: &huma.Schema{Type: "string"},
		Description: "相对登记前缀 " + route.Path + " 的路径（可含 /）；例 1.json 或子目录/资源.unity3d"}
}

func requestBody(route httpapi.Route, detail routeDetail) *huma.RequestBody {
	if route.Method != http.MethodPost {
		return nil
	}
	if detail.OctetStream {
		return &huma.RequestBody{Required: true, Description: "CN3.5 原始二进制帧；请使用协议验证器构造", Content: map[string]*huma.MediaType{
			"application/octet-stream": {Schema: &huma.Schema{Type: "string", Format: "binary"}},
		}}
	}
	if strings.HasPrefix(route.Path, httpapi.OpsPrefix) {
		return &huma.RequestBody{Description: "管理接口 JSON；字段尚未类型化，以 handler 为准", Content: map[string]*huma.MediaType{"application/json": {Schema: &huma.Schema{}}}}
	}
	props := map[string]*huma.Schema{}
	for _, p := range detail.Form {
		props[p.Name] = &huma.Schema{Type: "string", Description: p.Desc}
	}
	return &huma.RequestBody{Content: map[string]*huma.MediaType{"application/x-www-form-urlencoded": {Schema: &huma.Schema{Type: "object", Properties: props, AdditionalProperties: true}}}}
}

func summaryOf(note string) string {
	if i := strings.IndexAny(note, "—\n；;，。（("); i > 0 {
		return strings.TrimSpace(note[:i])
	}
	return note
}

// 操作 ID 保留尾斜杠与精确/前缀区别，避免别名冲突。
func operationID(route httpapi.Route) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s %s %t", route.Method, route.Path, route.Prefix)))
	return strings.ToLower(route.Method) + "_" + strings.ReplaceAll(route.Group, ".", "_") + fmt.Sprintf("_%x", h[:8])
}

func OpenAPIJSON(reg *httpapi.Registry, opt Options) ([]byte, error) {
	raw, err := json.MarshalIndent(Spec(reg, opt), "", "  ")
	return append(raw, '\n'), err
}

// Write 导出交接文件；3.0 降级和 Swagger UI 资源使用锁定依赖。
func Write(reg *httpapi.Registry, outDir string, opt Options) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	spec := Spec(reg, opt)
	raw, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return err
	}
	legacy, err := spec.Downgrade()
	if err != nil {
		return err
	}
	viewer, err := ViewerHTML(reg, opt)
	if err != nil {
		return err
	}
	files := map[string][]byte{"openapi.json": append(raw, '\n'), "openapi-3.0.json": append(legacy, '\n'), "index.html": viewer, "routes.md": RoutesMarkdown(reg, opt)}
	for _, name := range []string{"swagger-ui-bundle.js", "swagger-ui.css"} {
		asset, err := swaggerAsset(name)
		if err != nil {
			return err
		}
		files[name] = asset
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(outDir, name), data, 0o644); err != nil {
			return fmt.Errorf("写入 %s: %w", name, err)
		}
	}
	return nil
}

var groupTitles = map[string]string{
	"account":                        "账号帧（二进制信封）",
	"act_pages":                      "活动页配置",
	"albums":                         "图鉴/休息室",
	"app_conf_android.json.encode":   "启动配置",
	"asset_info_android.json.encode": "启动配置",
	"battles":                        "公会战",
	"campaigns":                      "活动/邀请",
	"cn35":                           "编解码自检",
	"comments":                       "评论",
	"events":                         "活动",
	"gachas":                         "抽卡",
	"guilds":                         "公会",
	"infos":                          "公告",
	"logs":                           "客户端日志上报",
	"medal_items":                    "勋章道具",
	"mercd":                          "运维端点",
	"month_card":                     "月卡",
	"open_box_activity":              "开箱活动",
	"quests":                         "关卡/战斗",
	"stories":                        "剧情",
	"task":                           "任务",
	"user_decks":                     "编队",
	"user_follows":                   "关注",
	"user_items":                     "道具",
	"user_monsters":                  "怪兽",
	"user_presents":                  "礼物箱",
	"user_skills":                    "技能",
	"user_units":                     "单位",
	"users":                          "用户",
	"waiting_users":                  "公会候补",
}
