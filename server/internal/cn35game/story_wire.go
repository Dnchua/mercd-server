// story_wire.go 故事阅读三接口（设计真源
// notes/proposed/2026-09-28-cn35-story-reader.md §2）：
//
//   - GET|POST /chapters/                Chapters 页 requireData.chapters；
//     章节行 chapter_select_option 经 PageBase.SetOption 深合并下发
//     Stories 页的 requireData 与 parts.ListParts.data_key
//     （IPageComponentExtentions.cs:944-960、JSONObject.MergeRecur 深合并），
//     是不改客户端的挂载点。
//   - GET|POST /stories/list?chapter_id=N  Stories 页列表（全部开放：
//     不读账号进度）；缓存键 stories_<N> 是 Story.ClosePage 续读的硬要求
//     （Story.cs:134,167）。
//   - GET|POST /stories/read/<id>.json   剧本读取（StoryReadDocument.cs:48-60
//     客户端硬编码路径）；命中原样下发 story_<id>.json 顶层
//     story/asset_names/actress_xys（无 data 信封），未命中返回明文 404。
//
// /stories/read/ 不用 gameHandler 包装：handler 内先尽力 DecodeGameRequest
// 填 c.Decoded（失败不致命），解码成功才走加密下发；404 一律明文小 JSON。
package cn35game

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/Dnchua/mercd-server/server/internal/cn35wire"
	"github.com/Dnchua/mercd-server/server/internal/httpapi"
)

// chaptersHandler GET|POST /chapters/：真实章节目录，替换
// module_requiredata.go 原空态。只下发 ≥1 话通过校验的章；
// fixture 缺失/全剔时回落 {"data":{"chapters":[]}}（客户端合法空态）。
func chaptersHandler(dep Deps, cat *storyCatalog) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		chapters := []any{}
		for _, ch := range cat.chapters {
			chapters = append(chapters, chapterDoc(ch))
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "chapters",
			"chapters": len(chapters), "stories": len(cat.stories),
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{"chapters": chapters},
		})
	})
}

// chaptersListHandler GET|POST /chapters/list：兼容 ChaptersAndStories（>=210）
// 路径，一次性下发 chapters.quest 与按 chapter_id 分组的 stories 映射。
func chaptersListHandler(dep Deps, cat *storyCatalog) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		questChapters := make([]any, 0, len(cat.chapters))
		storiesByChapter := make(map[string]any, len(cat.chapters))
		for i, ch := range cat.chapters {
			cd := chapterDoc(ch)
			cd["order_num"] = i + 1
			cd["main_story"] = true
			cd["released"] = true
			cd["story_type_id"] = 1
			questChapters = append(questChapters, cd)

			eps := cat.byChapter[ch.ID]
			rows := make([]any, 0, len(eps))
			for _, s := range eps {
				rows = append(rows, storyRowDoc(s.meta))
			}
			storiesByChapter[strconv.Itoa(ch.ID)] = rows
		}
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "chapters_list",
			"chapters": len(questChapters), "stories": len(cat.stories),
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{
				"chapters": map[string]any{
					"quest": questChapters,
					"event": []any{},
				},
				"stories": storiesByChapter,
			},
		})
	})
}

// chapterDoc 一条章节行：id/name/background_asset_name（行部件直读）
// + chapter_select_option（深合并进 Stories 页配置：requireData 拉取
// stories_<id>、ListParts.data_key、Header.title_right_label）。
func chapterDoc(ch storyIndexChapter) map[string]any {
	key := fmt.Sprintf("stories_%d", ch.ID)
	return map[string]any{
		"id":                    ch.ID,
		"name":                  ch.Name,
		"background_asset_name": ch.BackgroundAssetName,
		"chapter_select_option": map[string]any{
			"requireData": map[string]any{
				key: map[string]any{
					"path": fmt.Sprintf("/stories/list?chapter_id=%d", ch.ID),
				},
			},
			"parts": map[string]any{
				"ListParts": map[string]any{"data_key": key},
				"Header":    map[string]any{"title_right_label": ch.Name},
			},
		},
	}
}

// storiesListHandler GET|POST /stories/list?chapter_id=N：
// 合法 chapter_id → {"data":{"stories_<N>":[rows]}}（按 order_num 升序）；
// 缺/非数字 → {"data":{"stories":[]}}；数字但章未知 → stories_<N> 空表。
func storiesListHandler(dep Deps, cat *storyCatalog) httpapi.Handler {
	return gameHandler(dep, func(c *httpapi.Ctx, req *cn35wire.GameRequest) ([]byte, error) {
		rawID := reqParam(req, "chapter_id")
		chID, err := strconv.Atoi(rawID)
		c.Decoded = map[string]any{
			"cn35": true, "uid": req.UserID, "endpoint": "stories_list",
			"chapter_id": rawID,
		}
		if err != nil {
			return cn35wire.MarshalJSON(map[string]any{
				"data": map[string]any{"stories": []any{}},
			})
		}
		rows := []any{}
		for _, s := range cat.byChapter[chID] {
			rows = append(rows, storyRowDoc(s.meta))
		}
		return cn35wire.MarshalJSON(map[string]any{
			"data": map[string]any{fmt.Sprintf("stories_%d", chID): rows},
		})
	})
}

// storyRowDoc 一条 Stories 页列表行。story_read_additional_message 供
// RowClick 拼 PageMessage：必带 id/chapter_id（续读判定靠它，Story.cs:134）；
// title 剔除 &/=（PageMessage 按字面切分，见 cn35wire.MarshalJSON 注释
// 的 \u0026 事故），剔除后为空则省略 title。
func storyRowDoc(e storyIndexEntry) map[string]any {
	msg := fmt.Sprintf("id=%d&chapter_id=%d", e.ID, e.ChapterID)
	if title := sanitizePageMessageValue(e.Title); title != "" {
		msg += "&title=" + title
	}
	subTitle := e.SubTitle
	if subTitle == "" && e.OrderNum > 0 {
		subTitle = fmt.Sprintf("第%d话", e.OrderNum)
	}
	readTimingID := e.ReadTimingID
	if readTimingID <= 0 {
		readTimingID = 1
	}
	storyTypeID := e.StoryTypeID
	if storyTypeID <= 0 {
		storyTypeID = 1
	}
	return map[string]any{
		"id":                            e.ID,
		"chapter_id":                    e.ChapterID,
		"order_num":                     e.OrderNum,
		"title":                         e.Title,
		"sub_title":                     subTitle,
		"area_id":                       e.AreaID,
		"quest_id":                      e.QuestID,
		"read_timing_id":                readTimingID,
		"story_type_id":                 storyTypeID,
		"sub_story":                     false,
		"unit_asset_name":               "",
		"story_read_additional_message": msg,
	}
}

// sanitizePageMessageValue 去掉会击穿 PageMessage `&`/`=` 字面切分的字符。
func sanitizePageMessageValue(s string) string {
	s = strings.ReplaceAll(s, "&", "")
	s = strings.ReplaceAll(s, "=", "")
	return s
}

// storyReadPathRe 客户端硬编码剧本路径（StoryReadDocument.cs:48-60）。
var storyReadPathRe = regexp.MustCompile(`^/stories/read/(\d+)\.json$`)

// storyReadHandler GET|POST /stories/read/<id>.json（前缀路由）。
// 不走 gameHandler：先尽力解码填 c.Decoded；命中下发原文（解码成功→加密
// 通道，失败→明文——客户端仅按 ENCRYPTED 头判定，00-transport §1 两形皆合法）；
// 未命中/非法一律 HTTP 404 明文小 JSON（客户端 Document.Load 失败路径
// 弹错并 CancelShowPage 回列表——行为待实机确认，Note §2.3 未证项）。
func storyReadHandler(dep Deps, cat *storyCatalog) httpapi.Handler {
	return func(c *httpapi.Ctx) error {
		req, derr := cn35wire.DecodeGameRequest(c.R, c.Body, dep.Sessions)
		decoded := map[string]any{
			"cn35": true, "endpoint": "stories_read", "path": c.R.URL.Path,
		}
		var sess cn35wire.Session
		encrypted := false
		if derr == nil {
			sess = req.Session
			encrypted = true
			decoded["uid"] = req.UserID
			if q := req.Query; q != nil {
				for _, k := range []string{"id", "chapter_id", "title", "name"} {
					if v := q.Get(k); v != "" {
						decoded["query_"+k] = v
					}
				}
			}
		} else {
			decoded["decode_error"] = derr.Error()
		}
		var payload []byte
		if m := storyReadPathRe.FindStringSubmatch(c.R.URL.Path); m != nil {
			id, _ := strconv.Atoi(m[1])
			decoded["story_id"] = id
			if s := cat.stories[id]; s != nil {
				payload = s.payload
			}
		}
		c.Decoded = decoded
		if payload == nil {
			c.W.Header().Set("Content-Type", "application/json")
			c.W.WriteHeader(http.StatusNotFound)
			_, _ = c.W.Write([]byte(`{"status":"error","reason":"story not found"}`))
			return nil
		}
		return cn35wire.WriteGameResponse(c.W, payload, sess, encrypted)
	}
}
