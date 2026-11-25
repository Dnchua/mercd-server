// plan_test.go CAB 冲突判定表驱动测试。
package unitybundle

import (
	"crypto/md5"
	"encoding/hex"
	"reflect"
	"testing"
)

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestPlanCABRenames(t *testing.T) {
	cases := []struct {
		name     string
		embedded map[string][]string
		remote   map[string][]string
		want     map[string]map[string]string
	}{
		{
			name: "远端-远端共享 CAB：保留排序最前者（其 CAB 已是目标名）",
			remote: map[string][]string{
				"unit/unit_list_1194": {"CAB-unit_list_1194.Android"},
				"unit/unit_list_1197": {"CAB-unit_list_1194.Android"},
				"unit/unit_list_1198": {"CAB-unit_list_1194.Android"},
			},
			want: map[string]map[string]string{
				"unit/unit_list_1197": {"CAB-unit_list_1194.Android": "CAB-unit_list_1197.Android"},
				"unit/unit_list_1198": {"CAB-unit_list_1194.Android": "CAB-unit_list_1198.Android"},
			},
		},
		{
			name:     "远端-APK 撞名：远端改名",
			embedded: map[string][]string{"unit/unit_silhouette_402": {"CAB-shared.Android"}},
			remote: map[string][]string{
				"unit/unit_silhouette_403": {"CAB-shared.Android"},
			},
			want: map[string]map[string]string{
				"unit/unit_silhouette_403": {"CAB-shared.Android": "CAB-unit_silhouette_403.Android"},
			},
		},
		{
			name: "同名覆盖不算冲突（远端顶替 APK 同名行）",
			embedded: map[string][]string{
				"unit/unit_414": {"CAB-old.Android"},
				"unit/unit_415": {"CAB-415.Android"},
			},
			remote: map[string][]string{
				"unit/unit_414": {"CAB-new414.Android"},
			},
			want: nil,
		},
		{
			name: "目标名被占则回退 md5",
			remote: map[string][]string{
				"unit/a": {"CAB-x.Android"},
				"unit/b": {"CAB-x.Android"},
				"unit/c": {"CAB-a.Android"}, // 独占 a 的目标名
			},
			want: map[string]map[string]string{
				"unit/a": {"CAB-x.Android": "CAB-" + md5hex("unit/a")},
			},
		},
		{
			name: "已等于目标名且冲突：对方改名",
			remote: map[string][]string{
				"unit/unit_list_1235": {"CAB-unit_list_1235.Android"},
				"unit/unit_list_1236": {"CAB-unit_list_1235.Android"},
			},
			want: map[string]map[string]string{
				"unit/unit_list_1236": {"CAB-unit_list_1235.Android": "CAB-unit_list_1236.Android"},
			},
		},
		{
			name: "同 basename 不同目录撞自然目标名：后到者回退 md5",
			remote: map[string][]string{
				"unit/unit_large_1239":    {"CAB-unit_large_1239.Android"},
				"unit_hd/unit_large_1239": {"CAB-unit_large_1239.Android"},
			},
			want: map[string]map[string]string{
				"unit_hd/unit_large_1239": {"CAB-unit_large_1239.Android": "CAB-" + md5hex("unit_hd/unit_large_1239")},
			},
		},
		{
			name:   "不冲突不动",
			remote: map[string][]string{"unit/a": {"CAB-a.Android"}},
			want:   nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := PlanCABRenames(tc.embedded, tc.remote)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got=%v want=%v", got, tc.want)
			}
		})
	}
}
