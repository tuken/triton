package packet

import (
	"encoding/binary"
	"fmt"
	"time"

	"go.uber.org/zap/zapcore"
)

type ErrorReason byte

const (
	ReasonInvalidRequest     ErrorReason = 0x01
	ReasonDownlinking        ErrorReason = 0x02
	ReasonNotFoundDevice     ErrorReason = 0x06
	ReasonKeepAliveRequired  ErrorReason = 0x07
	ReasonNotAdvertingDevice ErrorReason = 0x08
	ReasonBusy               ErrorReason = 0x09
	ReasonTimeout            ErrorReason = 0x0A
	ReasonFailureCommand     ErrorReason = 0x0B
)

func (r ErrorReason) MarshalLogObject(enc zapcore.ObjectEncoder) error {

	switch r {

	case ReasonInvalidRequest:
		enc.AddString("reason", "不正なリクエスト")

	case ReasonDownlinking:
		enc.AddString("reason", "ダウンリンク処理中")

	case ReasonNotFoundDevice:
		enc.AddString("reason", "デバイス未登録")

	case ReasonKeepAliveRequired:
		enc.AddString("reason", "KeepAlive 必須")

	case ReasonNotAdvertingDevice:
		enc.AddString("reason", "アドバタイズ中でない")

	case ReasonBusy:
		enc.AddString("reason", "ビジー")

	case ReasonTimeout:
		enc.AddString("reason", "タイムアウト")

	case ReasonFailureCommand:
		enc.AddString("reason", "コマンド失敗")

	default:
		enc.AddString("reason", "不明なエラー")
	}

	return nil
}

// ErrorNotify エラー通知パケット（7バイト固定）
type ErrorNotify struct {
	ProtocolVersion byte        // Index 0: 0x01
	Type            byte        // Index 1: 0xFF
	UnixTime        uint32      // Index 2-5: Little Endian
	Reason          ErrorReason // Index 6
}

func (p *ErrorNotify) Unmarshal(buf []byte) error {

	if len(buf) < 7 {
		return fmt.Errorf("too short: %d bytes", len(buf))
	}

	p.ProtocolVersion = buf[0]
	p.Type = buf[1]
	p.UnixTime = binary.LittleEndian.Uint32(buf[2:6])
	p.Reason = ErrorReason(buf[6])

	return nil
}

func (p *ErrorNotify) FixedSize() int {
	return 7
}

func (p *ErrorNotify) VariableSize(fixed []byte) int {
	return 0
}

func (p *ErrorNotify) MarshalLogObject(enc zapcore.ObjectEncoder) error {

	enc.AddString("name", "Error 通知")
	enc.AddInt("protocolVersion", int(p.ProtocolVersion))
	enc.AddInt("type", int(p.Type))
	enc.AddTime("unixTime", time.Unix(int64(p.UnixTime), 0))
	enc.AddObject("reason", p.Reason)

	return nil
}
