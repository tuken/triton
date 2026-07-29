package packet

import (
	"encoding/binary"
	"fmt"
	"time"

	"github.com/tuken/triton/serial/packet/uplink"
	"go.uber.org/zap/zapcore"
)

const (
	HygrothermoSensorID = 0x0123
)

type SensorData interface {
	zapcore.ObjectMarshaler

	// PacketUnmarshal フレーム全体（固定部＋可変部）をパースする。
	PacketUnmarshal(senID, seqNo uint16, buf []byte) error
}

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
	// Data            []byte // Index 21-: 可変長データ（DataLength バイト）
	SensorData SensorData
}

func (p *UplinkNotify) PacketUnmarshal(buf []byte) error {

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
		// p.Data = append([]byte(nil), buf[21:21+int(p.DataLength)]...)

		switch p.SensorID {

		case HygrothermoSensorID:
			p.SensorData = &uplink.Hygrothermo{}

			if err := p.SensorData.PacketUnmarshal(p.SensorID, p.SequenceNo, buf[21:21+int(p.DataLength)]); err != nil {
				return err
			}
		}
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

func (p *UplinkNotify) MarshalLogObject(enc zapcore.ObjectEncoder) error {

	enc.AddString("name", "Uplink 通知")
	enc.AddInt("protocolVersion", int(p.ProtocolVersion))
	enc.AddInt("type", int(p.Type))
	enc.AddInt("dataLength", int(p.DataLength))
	enc.AddTime("unixTime", time.Unix(int64(p.UnixTime), 0))
	enc.AddString("deviceID", fmt.Sprintf("0x%016X", p.DeviceID))
	enc.AddString("sensorID", fmt.Sprintf("0x%04X", p.SensorID))
	enc.AddInt("rssi", int(p.Rssi))
	enc.AddInt("sequenceNo", int(p.SequenceNo))

	if p.SensorData != nil {
		enc.AddObject("sensorData", p.SensorData)
	}

	return nil
}
