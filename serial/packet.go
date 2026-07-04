package serial

import (
	"encoding/binary"
	"fmt"
)

const (
	ProtocolVersion = 0x01
	TypeInfoRequest = 0x01
)

// Marshaler は送信フレーム（リクエスト）が実装する。
type Marshaler interface {
	Marshal() []byte
}

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

type InfoCommand byte

const (
	CommandStop                 InfoCommand = 0x00
	CommandStart                InfoCommand = 0x01
	CommandGetVersion           InfoCommand = 0x02
	CommandGetDeviceListBase    InfoCommand = 0x03
	CommandGetDeviceListMax     int         = 99
	CommandGetScanMode          InfoCommand = 0x67
	CommandSetLongRangeMode     InfoCommand = 0x68
	CommandSetLegacyMode        InfoCommand = 0x69
	CommandRemoveAllDeviceList  InfoCommand = 0x6A
	CommandRemoveDeviceListBase InfoCommand = 0x6B
	CommandRemoveDeviceListMax  int         = 99
	CommandGetAllDeviceList     InfoCommand = 0xCF
	CommandKeepAlive            InfoCommand = 0xD0
)

// CommandGetDeviceList index(0..99) から CommandGetDeviceListN を生成する
func CommandGetDeviceList(index int) (InfoCommand, error) {

	if index < 0 || index > CommandGetDeviceListMax {
		return 0, fmt.Errorf("device list index out of range: %d", index)
	}

	return InfoCommand(int(CommandGetDeviceListBase) + index), nil
}

// GetDeviceListIndex CommandGetDeviceListN なら N と true を返す。
func (c InfoCommand) GetDeviceListIndex() (int, bool) {

	index := int(c) - int(CommandGetDeviceListBase)

	if index < 0 || index > CommandGetDeviceListMax {
		return 0, false
	}

	return index, true
}

// CommandRemoveDeviceList index(0..99) から CommandRemoveDeviceListN を生成する
func CommandRemoveDeviceList(index int) (InfoCommand, error) {

	if index < 0 || index > CommandRemoveDeviceListMax {
		return 0, fmt.Errorf("device list index out of range: %d", index)
	}

	return InfoCommand(int(CommandRemoveDeviceListBase) + index), nil
}

// RemoveDeviceListIndex CommandRemoveDeviceListN なら N と true を返す。
func (c InfoCommand) RemoveDeviceListIndex() (int, bool) {

	index := int(c) - int(CommandRemoveDeviceListBase)

	if index < 0 || index > CommandRemoveDeviceListMax {
		return 0, false
	}

	return index, true
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

// UplinkNotify アップリンク通知パケット（可変長、DataLengthで指定される）
type UplinkNotify struct {
	ProtocolVersion byte   // Index 0: 0x01
	Type            byte   // Index 1: 0x00
	DataLength      uint16 // Index 2-3: データ長（0..65535）
	UnixTime        uint32 // Index 4-7: Little Endian
	DeviceID        uint64 // Index 8-15: Little Endian
	SensorID        uint16 // Index 16-17: Little Endian
	Rssi            byte   // Index 18: RSSI値（-128..127）
	SequenceNo      uint16 // Index 19-20: Little Endian
	Data            []byte // Index 21-: 可変長データ（DataLength バイト）
}

func (p *UplinkNotify) Unmarshal(buf []byte) error {

	if len(buf) < 21 {
		return fmt.Errorf("too short: %d bytes", len(buf))
	}

	p.ProtocolVersion = buf[0]
	p.Type = buf[1]
	p.DataLength = binary.LittleEndian.Uint16(buf[2:4])
	p.UnixTime = binary.LittleEndian.Uint32(buf[4:8])
	p.DeviceID = binary.LittleEndian.Uint64(buf[8:16])
	p.SensorID = binary.LittleEndian.Uint16(buf[16:18])
	p.Rssi = buf[18]
	p.SequenceNo = binary.LittleEndian.Uint16(buf[19:21])

	if len(buf) >= 21+int(p.DataLength) {
		p.Data = append([]byte(nil), buf[21:21+int(p.DataLength)]...)
	}

	return nil
}

func (p *UplinkNotify) FixedSize() int {

	return 21
}

func (p *UplinkNotify) VariableSize(fixed []byte) int {

	// DataLength は固定部 Index 2-3（Little Endian）にある
	if len(fixed) < 4 {
		return 0
	}

	return int(binary.LittleEndian.Uint16(fixed[2:4]))
}

// DownlinkResponse ダウンリンク応答パケット（20バイト固定）
type DownlinkResponse struct {
	ProtocolVersion byte           // Index 0: 0x01
	Type            byte           // Index 1: 0x01
	UnixTime        uint32         // Index 2-5: Little Endian
	DeviceID        uint64         // Index 6-13: Little Endian
	SensorID        uint16         // Index 14-15: Little Endian
	SequenceNo      uint16         // Index 16-17: Little Endian
	Command         InfoCommand    // Index 18: コマンドコード
	Result          DownlinkResult // Index 19: 結果コード
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
	p.Command = InfoCommand(buf[18])
	p.Result = DownlinkResult(buf[19])

	return nil
}

func (p *DownlinkResponse) FixedSize() int {

	return 20
}

func (p *DownlinkResponse) VariableSize(fixed []byte) int {

	return 0
}

// InfoResponse Infoレスポンスパケット（可変長、Commandにより長さが決まる）
type InfoResponse struct {
	ProtocolVersion byte        // Index 0: 0x01
	Type            byte        // Index 1: 0x02
	UnixTime        uint32      // Index 2-5: Little Endian
	Command         InfoCommand // Index 6: コマンドコード
	RouterDeviceID  uint64      // Index 7-14: Little Endian
	Data            []byte      // Index 15-: 可変長データ（Commandで長さが決まる）
}

func (p *InfoResponse) Unmarshal(buf []byte) error {

	if len(buf) < 15 {
		return fmt.Errorf("too short: %d bytes", len(buf))
	}

	p.ProtocolVersion = buf[0]
	p.Type = buf[1]
	p.UnixTime = binary.LittleEndian.Uint32(buf[2:6])
	p.Command = InfoCommand(buf[6])
	p.RouterDeviceID = binary.LittleEndian.Uint64(buf[7:15])

	if len(buf) > 15 {
		p.Data = append([]byte(nil), buf[15:]...)
	}

	return nil
}

func (p *InfoResponse) FixedSize() int {
	return 15
}

func (p *InfoResponse) VariableSize(fixed []byte) int {

	// Command は固定部 Index 6 にある
	if len(fixed) < 7 {
		return 0
	}

	switch InfoCommand(fixed[6]) {

	case CommandStop, CommandStart, CommandSetLongRangeMode, CommandSetLegacyMode, CommandRemoveAllDeviceList:
		return 1

	case CommandGetScanMode:
		return 1

	case CommandGetVersion:
		return 3

	default:

		b := fixed[6]

		if b >= byte(CommandGetDeviceListBase) && b < byte(CommandGetDeviceListBase)+byte(CommandGetDeviceListMax) {
			return 9
		}

		if b >= byte(CommandRemoveDeviceListBase) && b < byte(CommandRemoveDeviceListBase)+byte(CommandRemoveDeviceListMax) {
			return 1
		}

		return 0
	}
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

// InfoRequest Infoリクエストパケット（11バイト固定）
type InfoRequest struct {
	ProtocolVersion byte        // Index 0: 0x01
	Type            byte        // Index 1: 0x01
	Command         InfoCommand // Index 2: コマンドコード
	LocalTime       uint32      // Index 3-6: Little Endian
	UnixTime        uint32      // Index 7-10: Little Endian
}

func (p *InfoRequest) Marshal() []byte {

	buf := make([]byte, 11)

	buf[0] = p.ProtocolVersion
	buf[1] = p.Type
	buf[2] = byte(p.Command)
	binary.LittleEndian.PutUint32(buf[3:7], p.LocalTime)
	binary.LittleEndian.PutUint32(buf[7:11], p.UnixTime)

	return buf
}

// DownlinkRequest ダウンリンクリクエストパケット（可変長、DataLengthで指定される）
type DownlinkRequest struct {
	ProtocolVersion byte        // Index 0: 0x01
	Type            byte        // Index 1: 0x00
	DataLength      uint16      // Index 2-3: データ長（0..65535）
	UnixTime        uint32      // Index 4-7: Little Endian
	DeviceID        uint64      // Index 8-15: Little Endian
	SensorID        uint16      // Index 16-17: Little Endian
	Command         InfoCommand // Index 18: コマンドコード
	SequenceNo      uint16      // Index 19-20: Little Endian
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
