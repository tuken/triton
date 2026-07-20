package packet

import (
	"encoding/binary"
	"fmt"
	"time"

	"go.uber.org/zap/zapcore"
)

type JIGInfoCommand byte

const (
	CommandStop                 JIGInfoCommand = 0x00
	CommandStart                JIGInfoCommand = 0x01
	CommandGetVersion           JIGInfoCommand = 0x02
	CommandGetDeviceListBase    JIGInfoCommand = 0x03
	CommandGetDeviceListMax     int            = 99
	CommandGetScanMode          JIGInfoCommand = 0x67
	CommandSetLongRangeMode     JIGInfoCommand = 0x68
	CommandSetLegacyMode        JIGInfoCommand = 0x69
	CommandRemoveAllDeviceList  JIGInfoCommand = 0x6A
	CommandRemoveDeviceListBase JIGInfoCommand = 0x6B
	CommandRemoveDeviceListMax  int            = 99
	CommandGetAllDeviceList     JIGInfoCommand = 0xCF
	CommandKeepAlive            JIGInfoCommand = 0xD0
)

// CommandGetDeviceList index(0..99) から CommandGetDeviceListN を生成する
func CommandGetDeviceList(index int) (JIGInfoCommand, error) {

	if index < 0 || index > CommandGetDeviceListMax {
		return 0, fmt.Errorf("device list index out of range: %d", index)
	}

	return JIGInfoCommand(int(CommandGetDeviceListBase) + index), nil
}

// GetDeviceListIndex CommandGetDeviceListN なら N と true を返す。
func (c JIGInfoCommand) GetDeviceListIndex() (int, bool) {

	index := int(c) - int(CommandGetDeviceListBase)

	if index < 0 || index > CommandGetDeviceListMax {
		return 0, false
	}

	return index, true
}

// CommandRemoveDeviceList index(0..99) から CommandRemoveDeviceListN を生成する
func CommandRemoveDeviceList(index int) (JIGInfoCommand, error) {

	if index < 0 || index > CommandRemoveDeviceListMax {
		return 0, fmt.Errorf("device list index out of range: %d", index)
	}

	return JIGInfoCommand(int(CommandRemoveDeviceListBase) + index), nil
}

// RemoveDeviceListIndex CommandRemoveDeviceListN なら N と true を返す。
func (c JIGInfoCommand) RemoveDeviceListIndex() (int, bool) {

	index := int(c) - int(CommandRemoveDeviceListBase)

	if index < 0 || index > CommandRemoveDeviceListMax {
		return 0, false
	}

	return index, true
}

func (c JIGInfoCommand) String() string {

	switch c {

	case CommandStop:
		return "Stop"

	case CommandStart:
		return "Start"

	case CommandGetVersion:
		return "GetVersion"

	case CommandGetScanMode:
		return "GetScanMode"

	case CommandSetLongRangeMode:
		return "SetLongRangeMode"

	case CommandSetLegacyMode:
		return "SetLegacyMode"

	case CommandRemoveAllDeviceList:
		return "RemoveAllDeviceList"

	case CommandGetAllDeviceList:
		return "GetAllDeviceList"

	case CommandKeepAlive:
		return "KeepAlive"

	default:
		if index, ok := c.GetDeviceListIndex(); ok {
			return fmt.Sprintf("GetDeviceList[%d]", index)
		}

		if index, ok := c.RemoveDeviceListIndex(); ok {
			return fmt.Sprintf("RemoveDeviceList[%d]", index)
		}

		return fmt.Sprintf("Unknown(0x%02X)", byte(c))
	}
}

// JIGInfoRequest Infoリクエストパケット（11バイト固定）
type JIGInfoRequest struct {
	ProtocolVersion byte           // Index 0: 0x01
	Type            byte           // Index 1: 0x01
	Command         JIGInfoCommand // Index 2: コマンドコード
	LocalTime       uint32         // Index 3-6: Little Endian
	UnixTime        uint32         // Index 7-10: Little Endian
}

func NewStopRequest() *JIGInfoRequest {

	return &JIGInfoRequest{
		ProtocolVersion: 0x01,
		Type:            0x01,
		Command:         CommandStop,
		LocalTime:       uint32(time.Now().Local().Unix()),
		UnixTime:        uint32(time.Now().Unix()),
	}
}

func NewStartRequest() *JIGInfoRequest {

	return &JIGInfoRequest{
		ProtocolVersion: 0x01,
		Type:            0x01,
		Command:         CommandStart,
		LocalTime:       uint32(time.Now().Local().Unix()),
		UnixTime:        uint32(time.Now().Unix()),
	}
}

func NewKeepAliveRequest() *JIGInfoRequest {

	return &JIGInfoRequest{
		ProtocolVersion: 0x01,
		Type:            0x01,
		Command:         CommandKeepAlive,
		LocalTime:       uint32(time.Now().Local().Unix()),
		UnixTime:        uint32(time.Now().Unix()),
	}
}

func (p *JIGInfoRequest) Marshal() []byte {

	buf := make([]byte, 11)

	buf[0] = p.ProtocolVersion
	buf[1] = p.Type
	buf[2] = byte(p.Command)
	binary.LittleEndian.PutUint32(buf[3:7], p.LocalTime)
	binary.LittleEndian.PutUint32(buf[7:11], p.UnixTime)

	return buf
}

// JIGInfoResponse Infoレスポンスパケット（可変長、Commandにより長さが決まる）
type JIGInfoResponse struct {
	ProtocolVersion byte           // Index 0: 0x01
	Type            byte           // Index 1: 0x02
	UnixTime        uint32         // Index 2-5: Little Endian
	Command         JIGInfoCommand // Index 6: コマンドコード
	RouterDeviceID  uint64         // Index 7-14: Little Endian
	Data            []byte         // Index 15-: 可変長データ（Commandで長さが決まる）
}

func (p *JIGInfoResponse) Unmarshal(buf []byte) error {

	if len(buf) < 15 {
		return fmt.Errorf("too short: %d bytes", len(buf))
	}

	p.ProtocolVersion = buf[0]
	p.Type = buf[1]
	p.UnixTime = binary.LittleEndian.Uint32(buf[2:6])
	p.Command = JIGInfoCommand(buf[6])
	p.RouterDeviceID = binary.LittleEndian.Uint64(buf[7:15])

	if len(buf) > 15 {
		p.Data = append([]byte(nil), buf[15:]...)
	}

	return nil
}

func (p *JIGInfoResponse) FixedSize() int {
	return 15
}

func (p *JIGInfoResponse) VariableSize(fixed []byte) int {

	// Command は固定部 Index 6 にある
	if len(fixed) < 7 {
		return 0
	}

	switch JIGInfoCommand(fixed[6]) {

	case CommandStop, CommandStart, CommandSetLongRangeMode, CommandSetLegacyMode, CommandRemoveAllDeviceList:
		return 1

	case CommandGetScanMode:
		return 1

	case CommandGetVersion:
		return 3

	default:

		b := fixed[6]

		if b >= byte(CommandGetDeviceListBase) && b <= byte(CommandGetDeviceListBase)+byte(CommandGetDeviceListMax) {
			return 9
		}

		if b >= byte(CommandRemoveDeviceListBase) && b <= byte(CommandRemoveDeviceListBase)+byte(CommandRemoveDeviceListMax) {
			return 1
		}

		return 0
	}
}

func (p *JIGInfoResponse) MarshalLogObject(enc zapcore.ObjectEncoder) error {

	enc.AddUint32("unixTime", p.UnixTime)
	enc.AddString("command", p.Command.String())
	enc.AddString("routerDeviceID", fmt.Sprintf("0x%016X", p.RouterDeviceID))
	enc.AddInt("data length", len(p.Data))

	return nil
}
