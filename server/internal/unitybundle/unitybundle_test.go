// unitybundle_test.go 合成 UnityWeb bundle 的 round-trip 测试
// （不依赖真实游戏资源）。
package unitybundle

import (
	"bytes"
	"testing"
)

// buildTestBundle 合成一个 format3 UnityWeb 单条目 bundle（复用 Pack）。
func buildTestBundle(t *testing.T, cabName string, payload []byte) []byte {
	t.Helper()
	b, err := Pack(cabName, payload)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// unpackTestBundle 解出（目录, 数据区）供断言。
func unpackTestBundle(t *testing.T, data []byte) ([]entry, []byte) {
	t.Helper()
	h, _, err := readHeader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := decodeLevel(data, h, 0)
	if err != nil {
		t.Fatal(err)
	}
	ents, err := parseDir(raw)
	if err != nil {
		t.Fatal(err)
	}
	return ents, raw
}

func TestCABNamesRoundTrip(t *testing.T) {
	payload := bytes.Repeat([]byte{1, 2, 3, 4, 5}, 5000)
	b := buildTestBundle(t, "CAB-unit_list_1194.Android", payload)

	names, err := CABNames(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "CAB-unit_list_1194.Android" {
		t.Fatalf("names=%v", names)
	}

	out, err := RenameCAB(b, "CAB-unit_list_1194.Android", "CAB-unit_list_1197.Android")
	if err != nil {
		t.Fatal(err)
	}
	names, err = CABNames(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "CAB-unit_list_1197.Android" {
		t.Fatalf("renamed names=%v", names)
	}
	ents, raw := unpackTestBundle(t, out)
	if len(ents) != 1 {
		t.Fatalf("ents=%v", ents)
	}
	got := raw[ents[0].offset : ents[0].offset+ents[0].size]
	if !bytes.Equal(got, payload) {
		t.Fatal("payload 改动")
	}
	// 头字段：format/版本串/魔数保留。
	if !bytes.HasPrefix(out, []byte("UnityWeb\x00")) {
		t.Fatal("魔数丢失")
	}
}

func TestRenameCABMissing(t *testing.T) {
	b := buildTestBundle(t, "CAB-a.Android", []byte("x"))
	if _, err := RenameCAB(b, "CAB-b.Android", "CAB-c.Android"); err == nil {
		t.Fatal("缺条目应报错")
	}
}
