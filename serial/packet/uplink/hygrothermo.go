package uplink

import (
	"encoding/binary"
	"fmt"
	"math"

	"go.uber.org/zap/zapcore"
)

type measureData struct {
	Temperature float32 // 摂氏（℃）
	Humidity    float32 // 相対湿度（％）
}

type Hygrothermo struct {
	BatteryLevel byte          // バッテリーレベル（％）
	Sampling     byte          // サンプリング周期（周波数）
	Time         uint32        // センサーリード時刻
	sampleNum    uint16        // サンプル数
	MeasureData  []measureData // 温湿度データ
}

func (h *Hygrothermo) PacketUnmarshal(senID, seqNo uint16, buf []byte) error {

	if len(buf) < 16 {
		return fmt.Errorf("too short: %d bytes", len(buf))
	}

	switch seqNo {

	case 0xffff:

		h.BatteryLevel = buf[0]
		h.Sampling = buf[1]
		h.Time = binary.LittleEndian.Uint32(buf[2:6])
		h.sampleNum = binary.LittleEndian.Uint16(buf[6:8])

		offset := 8

		for ; offset+8 <= len(buf); offset += 8 {

			h.MeasureData = append(h.MeasureData, measureData{
				Temperature: math.Float32frombits(binary.LittleEndian.Uint32(buf[offset : offset+4])),
				Humidity:    math.Float32frombits(binary.LittleEndian.Uint32(buf[offset+4 : offset+8])),
			})
		}

		if offset != len(buf) {
			return fmt.Errorf("invalid measure payload length: %d", len(buf)-8)
		}

	case 0x0000:

		h.BatteryLevel = buf[0]
		h.Sampling = buf[1]
		h.Time = binary.LittleEndian.Uint32(buf[2:6])
		h.sampleNum = binary.LittleEndian.Uint16(buf[6:8])

		offset := 8
		limit := 240
		if len(buf) < limit {
			limit = len(buf)
		}

		for ; offset+8 <= limit; offset += 8 {

			h.MeasureData = append(h.MeasureData, measureData{
				Temperature: math.Float32frombits(binary.LittleEndian.Uint32(buf[offset : offset+4])),
				Humidity:    math.Float32frombits(binary.LittleEndian.Uint32(buf[offset+4 : offset+8])),
			})
		}

		if err := h.PacketUnmarshal(senID, seqNo, buf[offset:]); err != nil {
			return err
		}

	default:

		sensorID := binary.LittleEndian.Uint16(buf[0:2])
		sequenceNo := binary.LittleEndian.Uint16(buf[2:4])

		if sensorID != senID {
			return fmt.Errorf("sensorID mismatch: %04x != %04x", sensorID, senID)
		}

		if sequenceNo != seqNo {
			return fmt.Errorf("sequenceNo mismatch: %04x != %04x", sequenceNo, seqNo)
		}

		offset := 4
		limit := 240
		if len(buf) < limit {
			limit = len(buf)
		}

		for ; offset+8 <= limit; offset += 8 {

			h.MeasureData = append(h.MeasureData, measureData{
				Temperature: math.Float32frombits(binary.LittleEndian.Uint32(buf[offset : offset+4])),
				Humidity:    math.Float32frombits(binary.LittleEndian.Uint32(buf[offset+4 : offset+8])),
			})
		}

		if err := h.PacketUnmarshal(senID, seqNo, buf[offset:]); err != nil {
			return err
		}
	}

	return nil
}

func (h *Hygrothermo) MarshalLogObject(enc zapcore.ObjectEncoder) error {

	enc.AddUint8("batteryLevel", h.BatteryLevel)
	enc.AddUint8("sampling", h.Sampling)
	enc.AddUint32("time", h.Time)
	enc.AddUint16("sampleNum", h.sampleNum)

	for i, m := range h.MeasureData {

		enc.AddObject(fmt.Sprintf("measureData[%d]", i), zapcore.ObjectMarshalerFunc(func(enc zapcore.ObjectEncoder) error {
			enc.AddFloat32("temperature", m.Temperature)
			enc.AddFloat32("humidity", m.Humidity)
			return nil
		}))
	}

	return nil
}
