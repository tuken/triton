package uplink

import (
	"encoding/binary"
	"fmt"
	"math"
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

func (h *Hygrothermo) Unmarshal(senID, seqNo uint16, buf []byte) error {

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

		for ; offset < len(buf); offset += 8 {

			h.MeasureData = append(h.MeasureData, measureData{
				Temperature: math.Float32frombits(binary.LittleEndian.Uint32(buf[offset : offset+4])),
				Humidity:    math.Float32frombits(binary.LittleEndian.Uint32(buf[offset+4 : offset+8])),
			})
		}

	case 0x0000:

		h.BatteryLevel = buf[0]
		h.Sampling = buf[1]
		h.Time = binary.LittleEndian.Uint32(buf[2:6])
		h.sampleNum = binary.LittleEndian.Uint16(buf[6:8])

		offset := 8

		for ; offset < 240; offset += 8 {

			h.MeasureData = append(h.MeasureData, measureData{
				Temperature: math.Float32frombits(binary.LittleEndian.Uint32(buf[offset : offset+4])),
				Humidity:    math.Float32frombits(binary.LittleEndian.Uint32(buf[offset+4 : offset+8])),
			})
		}

		h.Unmarshal(senID, seqNo, buf[offset:])

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

		for ; offset < 240; offset += 8 {

			h.MeasureData = append(h.MeasureData, measureData{
				Temperature: math.Float32frombits(binary.LittleEndian.Uint32(buf[offset : offset+4])),
				Humidity:    math.Float32frombits(binary.LittleEndian.Uint32(buf[offset+4 : offset+8])),
			})
		}

		h.Unmarshal(senID, seqNo, buf[offset:])
	}

	return nil
}
