package serial

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/tuken/triton/serial/packet"
)

const (
	ProtocolVersion = 0x01
)

const (
	TypeDownlinkRequest = 0x00
	TypeInfoRequest     = 0x01
	TypeDFURequest      = 0x03
)

// 受信フレームの Type（ヘッダ2バイト目）の値。
const (
	TypeUplinkNotify     byte = 0x00 // アップリンク通知（可変長）
	TypeDownlinkResponse byte = 0x01 // ダウンリンク応答（固定長）
	TypeJIGInfoResponse  byte = 0x02 // JIG Info レスポンス（可変長）
	TypeDFUResponse      byte = 0x03 // DFU レスポンス（固定長）
	TypeErrorNotify      byte = 0xFF // エラー通知（固定長）
)

// Frame 受信1フレームの共通インターフェース。
// フレームは先頭2バイト（[0]=ProtocolVersion, [1]=Type）が共通ヘッダで、
// 種別ごとに固定長部＋（必要なら）可変長部を持つ。可変長サイズは固定部の
// バイト列だけで決まるため、Unmarshal 前でも VariableSize を呼べる。
type Packet interface {

	// Unmarshal フレーム全体（固定部＋可変部）をパースする。
	Unmarshal(buf []byte) error

	// FixedSize 先に読み込むべき固定長部のサイズ（ヘッダ2バイトを含む）。
	FixedSize() int

	// VariableSize 固定部のバイト列 fixed から可変長部のサイズを返す。
	VariableSize(fixed []byte) int
}

// newPacketByType ヘッダ2バイト目（Type）から、対応する空の Packet を生成する。
// ここがプロトコルの「種別 → どの構造体で受けるか」のディスパッチ表になる。
func newPacketByType(typ byte) (Packet, error) {

	switch typ {

	case TypeUplinkNotify:
		return &packet.UplinkNotify{}, nil

	case TypeDownlinkResponse:
		return &DownlinkResponse{}, nil

	case TypeJIGInfoResponse:
		return &packet.JIGInfoResponse{}, nil

	case TypeDFUResponse:
		return &DFUResponse{}, nil

	case TypeErrorNotify:
		return &packet.ErrorNotify{}, nil

	default:
		return nil, fmt.Errorf("unknown frame type: 0x%02X", typ)
	}
}

// readPacket r から1フレームを読み出し、種別(Type)と Unmarshal 済みの Packet を返す。
// タイムアウトを扱わず、データが来るまで（またはポートが閉じられるまで）ブロックする。
//
//  1. フレーム先頭（ProtocolVersion + 既知 Type）まで同期する
//  2. Type から受け取る構造体を決める
//  3. 固定長部まで残りを読む
//  4. 固定部から可変長サイズを求め、あればその分を追加で読む
//  5. 全体を1回だけ Unmarshal する
func readPacket(r io.Reader) (byte, Packet, error) {

	// 1) フレーム先頭に同期する。
	//    ヘッダ2バイトを読み、[0]=ProtocolVersion かつ [1]=既知 Type に
	//    なるまで1バイトずつ読み進める。これにより、雑音（例: ASCII バナー）や
	//    直前フレームの長さズレで生じた同期ズレから、次の正しいフレーム境界へ
	//    復帰できる（未知バイトで受信を止めない）。
	header := make([]byte, 2)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}

	var f Packet

	for {

		if header[0] == ProtocolVersion {
			if fr, err := newPacketByType(header[1]); err == nil {
				f = fr
				break
			}
		}

		// 同期ズレ：ウィンドウを1バイトずらして先頭を探し直す。
		header[0] = header[1]
		if _, err := io.ReadFull(r, header[1:2]); err != nil {
			return 0, nil, err
		}
	}

	typ := header[1]

	// 3) 固定長部
	fixedSize := f.FixedSize()
	if fixedSize < 2 {
		return typ, nil, fmt.Errorf("invalid fixed size %d for type 0x%02X", fixedSize, typ)
	}

	buf := make([]byte, fixedSize)
	copy(buf, header)

	if _, err := io.ReadFull(r, buf[2:]); err != nil {
		return typ, nil, err
	}

	// 4) 可変長部
	if varSize := f.VariableSize(buf); varSize > 0 {

		full := make([]byte, fixedSize+varSize)
		copy(full, buf)

		if _, err := io.ReadFull(r, full[fixedSize:]); err != nil {
			return typ, nil, err
		}

		buf = full
	}

	// 5) Unmarshal
	if err := f.Unmarshal(buf); err != nil {
		return typ, nil, err
	}

	return typ, f, nil
}

// Marshaler 送信フレーム（リクエスト）が実装する。
type Marshaler interface {
	Marshal() []byte
}

type DownlinkResult byte

const (
	ResultSuccess            DownlinkResult = 0x00
	ResultInvalidSensor      DownlinkResult = 0x01
	ResultUnsupportedCommand DownlinkResult = 0x02
	ResultOutOfRange         DownlinkResult = 0x03
	ResultNotConnected       DownlinkResult = 0x04
	ResultTimeout            DownlinkResult = 0x05
	ResultNotFoundDevice     DownlinkResult = 0x07
	ResultBusyRouter         DownlinkResult = 0x08
	ResultBusyModule         DownlinkResult = 0x09
)

// DownlinkResponse ダウンリンク応答パケット（20バイト固定）
type DownlinkResponse struct {
	ProtocolVersion byte                  // Index 0: 0x01
	Type            byte                  // Index 1: 0x01
	UnixTime        uint32                // Index 2-5: Little Endian
	DeviceID        uint64                // Index 6-13: Little Endian
	SensorID        uint16                // Index 14-15: Little Endian
	SequenceNo      uint16                // Index 16-17: Little Endian
	Command         packet.JIGInfoCommand // Index 18: コマンドコード
	Result          DownlinkResult        // Index 19: 結果コード
}

func (p *DownlinkResponse) Unmarshal(buf []byte) error {

	if len(buf) < 20 {
		return fmt.Errorf("too short: %d bytes", len(buf))
	}

	p.ProtocolVersion = buf[0]
	p.Type = buf[1]
	p.UnixTime = binary.LittleEndian.Uint32(buf[2:6])
	p.DeviceID = binary.LittleEndian.Uint64(buf[6:14])
	p.SensorID = binary.LittleEndian.Uint16(buf[14:16])
	p.SequenceNo = binary.LittleEndian.Uint16(buf[16:18])
	p.Command = packet.JIGInfoCommand(buf[18])
	p.Result = DownlinkResult(buf[19])

	return nil
}

func (p *DownlinkResponse) FixedSize() int {

	return 20
}

func (p *DownlinkResponse) VariableSize(fixed []byte) int {

	return 0
}

// DFUResponse DFUレスポンスパケット（7バイト固定）
type DFUResponse struct {
	ProtocolVersion byte   // Index 0: 0x01
	Type            byte   // Index 1: 0x03
	UnixTime        uint32 // Index 2-5: Little Endian
	Result          byte   // Index 6
}

func (p *DFUResponse) Unmarshal(buf []byte) error {

	if len(buf) < 7 {
		return fmt.Errorf("too short: %d bytes", len(buf))
	}

	p.ProtocolVersion = buf[0]
	p.Type = buf[1]
	p.UnixTime = binary.LittleEndian.Uint32(buf[2:6])
	p.Result = buf[6]

	return nil
}

func (p *DFUResponse) FixedSize() int {
	return 7
}

func (p *DFUResponse) VariableSize(fixed []byte) int {
	return 0
}

// DownlinkRequest ダウンリンクリクエストパケット（可変長、DataLengthで指定される）
type DownlinkRequest struct {
	ProtocolVersion byte                  // Index 0: 0x01
	Type            byte                  // Index 1: 0x00
	DataLength      uint16                // Index 2-3: データ長（0..65535）
	UnixTime        uint32                // Index 4-7: Little Endian
	DeviceID        uint64                // Index 8-15: Little Endian
	SensorID        uint16                // Index 16-17: Little Endian
	Command         packet.JIGInfoCommand // Index 18: コマンドコード
	SequenceNo      uint16                // Index 19-20: Little Endian
}

func (p *DownlinkRequest) Marshal() []byte {

	buf := make([]byte, 21)

	buf[0] = p.ProtocolVersion
	buf[1] = p.Type
	binary.LittleEndian.PutUint16(buf[2:4], p.DataLength)
	binary.LittleEndian.PutUint32(buf[4:8], p.UnixTime)
	binary.LittleEndian.PutUint64(buf[8:16], p.DeviceID)
	binary.LittleEndian.PutUint16(buf[16:18], p.SensorID)
	buf[18] = byte(p.Command)
	binary.LittleEndian.PutUint16(buf[19:21], p.SequenceNo)

	return buf
}
