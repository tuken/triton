package packet

import (
	"encoding/binary"
	"fmt"
	"time"

	"go.uber.org/zap/zapcore"
)

// DownlinkRequest ダウンリンクリクエストパケット（可変長、DataLengthで指定される）
type DownlinkRequest struct {
	ProtocolVersion byte           // Index 0: 0x01
	Type            byte           // Index 1: 0x00
	DataLength      uint16         // Index 2-3: データ長（0..65535）
	UnixTime        uint32         // Index 4-7: Little Endian
	DeviceID        uint64         // Index 8-15: Little Endian
	SensorID        uint16         // Index 16-17: Little Endian
	Command         JIGInfoCommand // Index 18: コマンドコード
	SequenceNo      uint16         // Index 19-20: Little Endian
}

func (p *DownlinkRequest) PacketMarshal() []byte {

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

func (r DownlinkResult) String() string {

	switch r {

	case ResultSuccess:
		return "成功"

	case ResultInvalidSensor:
		return "センサーID不正"

	case ResultUnsupportedCommand:
		return "未サポートのコマンド"

	case ResultOutOfRange:
		return "設定値範囲外"

	case ResultNotConnected:
		return "接続失敗"

	case ResultTimeout:
		return "タイムアウト"

	case ResultNotFoundDevice:
		return "対象デバイスなし"

	case ResultBusyRouter:
		return "ルーターBusy"

	case ResultBusyModule:
		return "モジュールBusy"

	default:
		return fmt.Sprintf("Unknown(0x%02X)", byte(r))
	}
}

// DownlinkResponse ダウンリンク応答パケット（20バイト固定）
type DownlinkResponse struct {
	ProtocolVersion byte           // Index 0: 0x01
	Type            byte           // Index 1: 0x01
	UnixTime        uint32         // Index 2-5: Little Endian
	DeviceID        uint64         // Index 6-13: Little Endian
	SensorID        uint16         // Index 14-15: Little Endian
	SequenceNo      uint16         // Index 16-17: Little Endian
	Command         JIGInfoCommand // Index 18: コマンドコード
	Result          DownlinkResult // Index 19: 結果コード
}

func (p *DownlinkResponse) PacketUnmarshal(buf []byte) error {

	if len(buf) < 20 {
		return fmt.Errorf("too short: %d bytes", len(buf))
	}

	p.ProtocolVersion = buf[0]
	p.Type = buf[1]
	p.UnixTime = binary.LittleEndian.Uint32(buf[2:6])
	p.DeviceID = binary.LittleEndian.Uint64(buf[6:14])
	p.SensorID = binary.LittleEndian.Uint16(buf[14:16])
	p.SequenceNo = binary.LittleEndian.Uint16(buf[16:18])
	p.Command = JIGInfoCommand(buf[18])
	p.Result = DownlinkResult(buf[19])

	return nil
}

func (p *DownlinkResponse) FixedSize() int {

	return 20
}

func (p *DownlinkResponse) VariableSize(fixed []byte) int {

	return 0
}

func (p *DownlinkResponse) MarshalLogObject(enc zapcore.ObjectEncoder) error {

	enc.AddString("name", "Downlink レスポンス")
	enc.AddUint8("protocolVersion", p.ProtocolVersion)
	enc.AddUint8("type", p.Type)
	enc.AddTime("unixTime", time.Unix(int64(p.UnixTime), 0).UTC())
	enc.AddString("deviceID", fmt.Sprintf("0x%016X", p.DeviceID))
	enc.AddString("sensorID", fmt.Sprintf("0x%04X", p.SensorID))
	enc.AddUint16("sequenceNo", p.SequenceNo)
	enc.AddString("command", p.Command.String())
	enc.AddString("result", p.Result.String())

	return nil
}
