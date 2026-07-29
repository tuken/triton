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

func (r ErrorReason) String() string {

	switch r {

	case ReasonInvalidRequest:
		return "不正なリクエスト"

	case ReasonDownlinking:
		return "ダウンリンク処理中"

	case ReasonNotFoundDevice:
		return "デバイスID未登録"

	case ReasonKeepAliveRequired:
		return "KeepAlive要求"

	case ReasonNotAdvertingDevice:
		return "アドバタイズ中でない"

	case ReasonBusy:
		return "Busy"

	case ReasonTimeout:
		return "タイムアウト"

	case ReasonFailureCommand:
		return "コマンド失敗"

	default:
		return fmt.Sprintf("Unknown(0x%02X)", byte(r))
	}
}

// ErrorNotify エラー通知パケット（7バイト固定）
type ErrorNotify struct {
	ProtocolVersion byte        // Index 0: 0x01
	Type            byte        // Index 1: 0xFF
	UnixTime        uint32      // Index 2-5: Little Endian
	Reason          ErrorReason // Index 6
}

func (p *ErrorNotify) PacketUnmarshal(buf []byte) error {

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
	enc.AddUint8("protocolVersion", p.ProtocolVersion)
	enc.AddUint8("type", p.Type)
	enc.AddTime("unixTime", time.Unix(int64(p.UnixTime), 0).UTC())
	enc.AddString("reason", p.Reason.String())

	return nil
}
