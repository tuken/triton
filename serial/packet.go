package serial

import (
	"fmt"
	"io"

	"github.com/tuken/triton/serial/packet"
	"go.uber.org/zap/zapcore"
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
// バイト列だけで決まるため、PacketUnmarshal 前でも VariableSize を呼べる。
type Responder interface {

	// PacketUnmarshal フレーム全体（固定部＋可変部）をパースする。
	PacketUnmarshal(buf []byte) error

	// FixedSize 先に読み込むべき固定長部のサイズ（ヘッダ2バイトを含む）。
	FixedSize() int

	// VariableSize 固定部のバイト列 fixed から可変長部のサイズを返す。
	VariableSize(fixed []byte) int

	zapcore.ObjectMarshaler
}

// newPacketByType ヘッダ2バイト目（Type）から、対応する空の Packet を生成する。
// ここがプロトコルの「種別 → どの構造体で受けるか」のディスパッチ表になる。
func newPacketByType(typ byte) (Responder, error) {

	switch typ {

	case TypeUplinkNotify:
		return &packet.UplinkNotify{}, nil

	case TypeDownlinkResponse:
		return &packet.DownlinkResponse{}, nil

	case TypeJIGInfoResponse:
		return &packet.JIGInfoResponse{}, nil

	case TypeDFUResponse:
		return &packet.DFUResponse{}, nil

	case TypeErrorNotify:
		return &packet.ErrorNotify{}, nil

	default:
		return nil, fmt.Errorf("unknown frame type: 0x%02X", typ)
	}
}

// readPacket r から1フレームを読み出し、種別(Type)と PacketUnmarshal 済みの Packet を返す。
// タイムアウトを扱わず、データが来るまで（またはポートが閉じられるまで）ブロックする。
//
//  1. フレーム先頭（ProtocolVersion + 既知 Type）まで同期する
//  2. Type から受け取る構造体を決める
//  3. 固定長部まで残りを読む
//  4. 固定部から可変長サイズを求め、あればその分を追加で読む
//  5. 全体を1回だけ PacketUnmarshal する
func readPacket(r io.Reader) (byte, Responder, error) {

	// 1) フレーム先頭に同期する。
	//    ヘッダ2バイトを読み、[0]=ProtocolVersion かつ [1]=既知 Type に
	//    なるまで1バイトずつ読み進める。これにより、雑音（例: ASCII バナー）や
	//    直前フレームの長さズレで生じた同期ズレから、次の正しいフレーム境界へ
	//    復帰できる（未知バイトで受信を止めない）。
	header := make([]byte, 2)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}

	var f Responder

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

	// 5) PacketUnmarshal
	if err := f.PacketUnmarshal(buf); err != nil {
		return typ, nil, err
	}

	return typ, f, nil
}

// Marshaler 送信フレーム（リクエスト）が実装する。
type Requestable interface {
	PacketMarshal() []byte
	zapcore.ObjectMarshaler
}
