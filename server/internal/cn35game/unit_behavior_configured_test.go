// unit_behavior_configured_test.go 卡池准入谓词：unit_overrides 官方样本
// 视同已配置（修复前 MemorialUnitConfigured 只查 unit_memorial，8 个官方
// 样本被误挡在池外）。
package cn35game

import (
	"reflect"
	"testing"
)

func TestUnitBehaviorConfigured(t *testing.T) {
	// 152/16：unit_memorial 无条目，仅 unit_overrides 命中 → 仍算已配置。
	for _, id := range []int{152, 16} {
		if MemorialUnitConfigured(id) {
			t.Fatalf("前提不成立：%d 不应有 unit_memorial 条目", id)
		}
		if !UnitBehaviorConfigured(id) {
			t.Fatalf("UnitBehaviorConfigured(%d)=false，unit_overrides 命中应视同已配置", id)
		}
	}
	// 343：unit_overrides/unit_memorial 均无 → 未配置。
	if UnitBehaviorConfigured(343) {
		t.Fatal("UnitBehaviorConfigured(343)=true，两层均无应为 false")
	}
}

func TestOfficialSampleUnitIDs(t *testing.T) {
	got := OfficialSampleUnitIDs()
	want := []int{16, 152, 156, 218, 296, 336, 348, 375}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OfficialSampleUnitIDs=%v want %v", got, want)
	}
}
