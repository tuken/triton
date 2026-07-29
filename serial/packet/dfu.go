package packet

import (
	"encoding/binary"
	"fmt"
	"time"

	"go.uber.org/zap/zapcore"
)

// DFUResponse DFUレスポンスパケット（7バイト固定）
type DFUResponse struct {
	ProtocolVersion byte   // Index 0: 0x01
	Type            byte   // Index 1: 0x03
	UnixTime        uint32 // Index 2-5: Little Endian
	Result          byte   // Index 6
}

func (p *DFUResponse) PacketUnmarshal(buf []byte) error {

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

func (p *DFUResponse) MarshalLogObject(enc zapcore.ObjectEncoder) error {

	enc.AddString("name", "DFUレスポンス")
	enc.AddInt("protocolVersion", int(p.ProtocolVersion))
	enc.AddInt("type", int(p.Type))
	enc.AddTime("unixTime", time.Unix(int64(p.UnixTime), 0))

	if p.Result == 0 {
		enc.AddString("result", "失敗")
	} else {
		enc.AddString("result", "成功")
	}

	return nil
}
