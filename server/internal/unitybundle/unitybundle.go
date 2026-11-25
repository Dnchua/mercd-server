// unitybundle.go Unity 4 asset bundle（format version 3，UnityWeb/UnityRaw）
// 目录项（内部归档名，"CAB-…"）读取与改名的最小实现。
//
// 布局（client/decoded 3732 个 Android bundle 与 inputs/files 实测一致）：
//
//	魔数 cstring "UnityWeb"|"UnityRaw"
//	u32BE format version（=3）
//	cstring player 版本（"3.x.x"）+ cstring Unity 版本（"4.6.7f1"）
//	u32BE completeFileSize / headerSize(0x3c) / numberOfLevelsToDownload(1) /
//	  levelCount(1)，随后每级 u32BE compressedSize + u32BE uncompressedSize
//	u32BE completeFileSize（format>=2 重复一次）
//	u32BE fileInfoHeaderSize（format>=3；解压后目录块占用的字节数）
//	填充到 headerSize
//
// level 数据：UnityWeb 为 LZMA-alone（13 字节头：props、4 字节 LE dict
// size、8 字节 LE 未压缩长度，无 EOS 标记）；UnityRaw 为原始字节。
// 解压后：u32BE 条目数，每条 = cstring 名 + u32BE offset + u32BE size，
// 目录块整体 4 字节对齐（fileInfoHeaderSize 即对齐后长度），各条目数据
// 依次排在目录块之后，offset 相对解压流起点且 4 字节对齐。
package unitybundle

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/ulikunitz/xz/lzma"
)

// header bundle 外层头中需要保留/重写的字段。
type header struct {
	magic      string // UnityWeb 或 UnityRaw
	format     uint32
	playerVer  string
	engineVer  string
	headerSize uint32
	compSize   uint32 // 第 0 级压缩后大小
	uncompSize uint32
	fihs       uint32 // fileInfoHeaderSize
}

// entry 解压流中的一个归档条目。
type entry struct {
	name   string
	offset uint32
	size   uint32
	data   []byte // rename 路径下持有解压数据
}

// lzmaParams 原文件 LZMA 参数（重压缩时原样复用）。
type lzmaParams struct {
	props   byte
	dictCap uint32
	raw     bool // UnityRaw：未压缩
}

// readCString 从 p 读 NUL 结尾字符串。
func readCString(b []byte, p *int) (string, error) {
	i := bytes.IndexByte(b[*p:], 0)
	if i < 0 {
		return "", fmt.Errorf("unitybundle: 头部 cstring 未终止 @%d", *p)
	}
	s := string(b[*p : *p+i])
	*p += i + 1
	return s, nil
}

// readHeader 从 r 解析外层头，返回 header 与 level 数据读取器。
// r 必须可 Seek（RenameCAB 传 bytes.Reader；CABNames 传带缓存的文件）。
func readHeader(r io.Reader) (header, []byte, error) {
	var h header
	sig, err := readCStringAt(r)
	if err != nil {
		return h, nil, err
	}
	h.magic = sig
	if sig != "UnityWeb" && sig != "UnityRaw" {
		return h, nil, fmt.Errorf("unitybundle: 魔数非法 %q", sig)
	}
	var u32 uint32
	if err = binary.Read(r, binary.BigEndian, &u32); err != nil {
		return h, nil, err
	}
	h.format = u32
	if h.format < 1 || h.format > 3 {
		return h, nil, fmt.Errorf("unitybundle: 未支持的 format version %d", h.format)
	}
	var err2 error
	if h.playerVer, err2 = readCStringAt(r); err2 != nil {
		return h, nil, err2
	}
	if h.engineVer, err2 = readCStringAt(r); err2 != nil {
		return h, nil, err2
	}
	var fixed [4]uint32
	if err = binary.Read(r, binary.BigEndian, &fixed); err != nil {
		return h, nil, err
	}
	completeFileSize, headerSize := fixed[0], fixed[1]
	h.headerSize = headerSize
	nlvl, lc := fixed[2], fixed[3]
	if nlvl != 1 || lc != 1 {
		return h, nil, fmt.Errorf("unitybundle: 未支持的多 level 形态 nlvl=%d lc=%d", nlvl, lc)
	}
	var lvl [2]uint32
	if err = binary.Read(r, binary.BigEndian, &lvl); err != nil {
		return h, nil, err
	}
	h.compSize, h.uncompSize = lvl[0], lvl[1]
	if h.format >= 2 {
		if err = binary.Read(r, binary.BigEndian, &u32); err != nil {
			return h, nil, err
		}
	}
	if h.format >= 3 {
		if err = binary.Read(r, binary.BigEndian, &h.fihs); err != nil {
			return h, nil, err
		}
	}
	_ = completeFileSize
	return h, nil, nil
}

func readCStringAt(r io.Reader) (string, error) {
	var buf bytes.Buffer
	var one [1]byte
	for {
		if _, err := r.Read(one[:]); err != nil {
			return "", err
		}
		if one[0] == 0 {
			return buf.String(), nil
		}
		buf.WriteByte(one[0])
	}
}

// headerLen 外层头总长（含 cstring）。解析后由 headerSize 字段给出数据起点。
// decodeLevel 读取 headerSize 之后的 level 压缩数据并解压。
// limit>0 时只解压前 limit 字节（CABNames 只取目录块）。
func decodeLevel(data []byte, h header, limit int64) ([]byte, lzmaParams, error) {
	var p lzmaParams
	if len(data) < int(h.headerSize) {
		return nil, p, fmt.Errorf("unitybundle: 文件短于 headerSize %d", h.headerSize)
	}
	comp := data[h.headerSize:]
	if int64(len(comp)) > int64(h.compSize) {
		comp = comp[:h.compSize]
	}
	if h.magic == "UnityRaw" {
		p.raw = true
		out := comp
		if limit > 0 && int64(len(out)) > limit {
			out = out[:limit]
		}
		return out, p, nil
	}
	if len(comp) < 13 {
		return nil, p, fmt.Errorf("unitybundle: LZMA 头不足 13 字节")
	}
	p.props = comp[0]
	p.dictCap = binary.LittleEndian.Uint32(comp[1:5])
	zr, err := lzma.ReaderConfig{DictCap: int(p.dictCap)}.NewReader(bytes.NewReader(comp))
	if err != nil {
		return nil, p, fmt.Errorf("unitybundle: LZMA 初始化: %w", err)
	}
	src := io.Reader(zr)
	if limit > 0 {
		src = io.LimitReader(zr, limit)
	}
	out, err := io.ReadAll(src)
	if err != nil {
		return nil, p, fmt.Errorf("unitybundle: LZMA 解压: %w", err)
	}
	return out, p, nil
}

// parseDir 解析解压流的目录块（不校验数据区完整性）。
func parseDir(raw []byte) ([]entry, error) {
	if len(raw) < 4 {
		return nil, fmt.Errorf("unitybundle: 目录块不足 4 字节")
	}
	n := binary.BigEndian.Uint32(raw[:4])
	p := 4
	ents := make([]entry, 0, n)
	for i := uint32(0); i < n; i++ {
		name, err := readCString(raw, &p)
		if err != nil {
			return nil, err
		}
		if p+8 > len(raw) {
			return nil, fmt.Errorf("unitybundle: 目录块截断 @%d", p)
		}
		off := binary.BigEndian.Uint32(raw[p : p+4])
		sz := binary.BigEndian.Uint32(raw[p+4 : p+8])
		p += 8
		ents = append(ents, entry{name: name, offset: off, size: sz})
	}
	return ents, nil
}

// CABNames 只解码目录块，返回 bundle 内部归档文件名列表。
// 供批量扫描用：只解压 fileInfoHeaderSize 所需前缀，不整包解压。
func CABNames(r io.Reader) ([]string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	h, _, err := readHeader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	limit := int64(h.fihs)
	if limit <= 0 {
		limit = 1 << 20
	}
	raw, _, err := decodeLevel(data, h, limit+16)
	if err != nil {
		return nil, err
	}
	ents, err := parseDir(raw)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(ents))
	for i, e := range ents {
		names[i] = e.name
	}
	return names, nil
}

func align4(n int) int { return (n + 3) &^ 3 }

// Pack 合成一个 format3 UnityWeb 单条目 bundle（LC3/LP0/PB2、8MB dict、
// 显式未压缩长度；headerSize=0x3c，player "3.x.x"，engine "4.6.7f1"）。
// 供测试与工具造最小可用 bundle。
func Pack(cabName string, payload []byte) ([]byte, error) {
	dirLen := 4 + len(cabName) + 1 + 8
	fihs := align4(dirLen)
	var raw bytes.Buffer
	binary.Write(&raw, binary.BigEndian, uint32(1))
	raw.WriteString(cabName)
	raw.WriteByte(0)
	binary.Write(&raw, binary.BigEndian, uint32(fihs))
	binary.Write(&raw, binary.BigEndian, uint32(len(payload)))
	for raw.Len() < fihs {
		raw.WriteByte(0)
	}
	raw.Write(payload)

	var comp bytes.Buffer
	props := lzma.Properties{LC: 3, LP: 0, PB: 2}
	zw, err := lzma.WriterConfig{
		Properties: &props, DictCap: 8 * 1024 * 1024,
		Size: int64(raw.Len()),
	}.NewWriter(&comp)
	if err != nil {
		return nil, err
	}
	if _, err = zw.Write(raw.Bytes()); err != nil {
		return nil, err
	}
	if err = zw.Close(); err != nil {
		return nil, err
	}

	var hdr bytes.Buffer
	hdr.WriteString("UnityWeb")
	hdr.WriteByte(0)
	binary.Write(&hdr, binary.BigEndian, uint32(3))
	hdr.WriteString("3.x.x")
	hdr.WriteByte(0)
	hdr.WriteString("4.6.7f1")
	hdr.WriteByte(0)
	complete := uint32(0x3c + comp.Len())
	for _, v := range []uint32{complete, 0x3c, 1, 1,
		uint32(comp.Len()), uint32(raw.Len()), complete, uint32(fihs)} {
		binary.Write(&hdr, binary.BigEndian, v)
	}
	for hdr.Len() < 0x3c {
		hdr.WriteByte(0)
	}
	return append(hdr.Bytes(), comp.Bytes()...), nil
}

// RenameCAB 整包解压 → 把目录中名为 oldName 的条目改为 newName →
// 重算目录块/各 size → 按原 LZMA 参数重压缩 → 重写外层头。
// oldName 不存在时返回错误；改的是内部归档名，条目数据原样保留。
func RenameCAB(data []byte, oldName, newName string) ([]byte, error) {
	h, _, err := readHeader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	raw, p, err := decodeLevel(data, h, 0)
	if err != nil {
		return nil, err
	}
	ents, err := parseDir(raw)
	if err != nil {
		return nil, err
	}
	found := false
	for i := range ents {
		if ents[i].name == oldName {
			ents[i].name = newName
			found = true
		}
		if int(ents[i].offset)+int(ents[i].size) > len(raw) {
			return nil, fmt.Errorf("unitybundle: 条目 %q 数据越界", ents[i].name)
		}
		ents[i].data = raw[ents[i].offset : ents[i].offset+ents[i].size]
	}
	if !found {
		return nil, fmt.Errorf("unitybundle: 归档内无条目 %q", oldName)
	}

	// 重建解压流：目录块（4 对齐）+ 各条目数据（offset 4 对齐）。
	dirLen := 4
	for _, e := range ents {
		dirLen += len(e.name) + 1 + 8
	}
	fihs := align4(dirLen)
	out := make([]byte, 0, fihs)
	var dir bytes.Buffer
	binary.Write(&dir, binary.BigEndian, uint32(len(ents)))
	dataOff := fihs
	body := make([]byte, 0)
	for i := range ents {
		dir.WriteString(ents[i].name)
		dir.WriteByte(0)
		binary.Write(&dir, binary.BigEndian, uint32(dataOff))
		binary.Write(&dir, binary.BigEndian, ents[i].size)
		body = append(body, ents[i].data...)
		dataOff += int(ents[i].size)
		if i+1 < len(ents) { // 条目间 4 字节对齐；末条目后不补
			for dataOff%4 != 0 {
				body = append(body, 0)
				dataOff++
			}
		}
	}
	out = append(out, dir.Bytes()...)
	for len(out) < fihs {
		out = append(out, 0)
	}
	out = append(out, body...)

	// 重压缩（原 props/dict；显式未压缩长度，不写 EOS）。
	var comp []byte
	if p.raw {
		comp = out
	} else {
		var buf bytes.Buffer
		props, err := lzma.PropertiesForCode(p.props)
		if err != nil {
			return nil, fmt.Errorf("unitybundle: LZMA props 非法: %w", err)
		}
		cfg := lzma.WriterConfig{
			Properties: &props,
			DictCap:    int(p.dictCap),
			Size:       int64(len(out)),
		}
		zw, err := cfg.NewWriter(&buf)
		if err != nil {
			return nil, fmt.Errorf("unitybundle: LZMA 压缩初始化: %w", err)
		}
		if _, err = zw.Write(out); err != nil {
			return nil, fmt.Errorf("unitybundle: LZMA 压缩: %w", err)
		}
		if err = zw.Close(); err != nil {
			return nil, fmt.Errorf("unitybundle: LZMA 收尾: %w", err)
		}
		comp = buf.Bytes()
	}

	// 重写外层头：魔数/版本字符串保留，各 size 字段重算。
	var hdr bytes.Buffer
	hdr.WriteString(h.magic)
	hdr.WriteByte(0)
	binary.Write(&hdr, binary.BigEndian, h.format)
	hdr.WriteString(h.playerVer)
	hdr.WriteByte(0)
	hdr.WriteString(h.engineVer)
	hdr.WriteByte(0)
	complete := h.headerSize + uint32(len(comp))
	binary.Write(&hdr, binary.BigEndian, complete)
	binary.Write(&hdr, binary.BigEndian, h.headerSize)
	binary.Write(&hdr, binary.BigEndian, uint32(1))
	binary.Write(&hdr, binary.BigEndian, uint32(1))
	binary.Write(&hdr, binary.BigEndian, uint32(len(comp)))
	binary.Write(&hdr, binary.BigEndian, uint32(len(out)))
	if h.format >= 2 {
		binary.Write(&hdr, binary.BigEndian, complete)
	}
	if h.format >= 3 {
		binary.Write(&hdr, binary.BigEndian, uint32(fihs))
	}
	for hdr.Len() < int(h.headerSize) {
		hdr.WriteByte(0)
	}
	if hdr.Len() != int(h.headerSize) {
		return nil, fmt.Errorf("unitybundle: 重写头长 %d 超出 headerSize %d", hdr.Len(), h.headerSize)
	}
	return append(hdr.Bytes(), comp...), nil
}
