package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/signal"
	"syscall"
	"time"

	myctx "github.com/tuken/triton/context"
	"github.com/tuken/triton/logger"
	"github.com/tuken/triton/serial"
	"github.com/tuken/triton/usb"
	goser "go.bug.st/serial"
)

func main() {

	// Ctrl-C / SIGTERM が来ると ctx がキャンセルされる
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log := logger.NewLogger()
	ctx = context.WithValue(ctx, myctx.LoggerKey, log)

	watch := usb.NewWatch(ctx)

	portName := watch.Find("BraveJIG Router")
	if portName == "" {

		log.Infow("USB未接続")
		return
	}

	log.Infow("アプリケーション起動")

	com := serial.NewCom(ctx)

	com.Handle(serial.TypeUplinkNotify, handleUplinkNotify)
	com.Handle(serial.TypeDownlinkResponse, handleDownlinkResponse)
	com.Handle(serial.TypeInfoResponse, handleInfoResponse)
	com.Handle(serial.TypeDFUResponse, handleDFUResponse)
	com.Handle(serial.TypeErrorNotify, handleErrorNotify)

	// COMポートをオープン
	if err := com.Connect(portName, &goser.Mode{
		BaudRate: 115200,
		DataBits: 8,
		Parity:   goser.NoParity,
		StopBits: goser.OneStopBit,
	}); err != nil {
		log.Fatalw("USB接続エラー", "error", err)
		return
	}

	defer com.Disconnect()

	com.Run()
}

func handleUplinkNotify(c *serial.Com, f serial.Frame) {

	log := myctx.MustLogger(c.Context())

	notify, ok := f.(*serial.UplinkNotify)
	if !ok {
		log.Errorw("Invalid frame type")
		return
	}

	log.Infow("UplinkNotify 受信", "data length", notify.DataLength, "unix time", time.Unix(int64(notify.UnixTime), 0).UTC(), "device id", fmt.Sprintf("%016x", notify.DeviceID), "sensor id", fmt.Sprintf("%04x", notify.SensorID), "rssi", notify.Rssi, "sequence no", fmt.Sprintf("%04x", notify.SequenceNo))

	if notify.SensorID == 0x0123 {

		type Thermo struct {
			BatteryLevel byte    `json:"battery_level"`
			Sampling     uint16  `json:"sampling"`
			Time         uint32  `json:"time"`
			SampleNum    uint16  `json:"sample_num"`
			Temperature  float32 `json:"temperature"`
			Humidity     float32 `json:"humidity"`
		}

		if len(notify.Data) >= 16 {

			th := Thermo{
				BatteryLevel: notify.Data[0],
				Sampling:     binary.LittleEndian.Uint16(notify.Data[1:3]),
				Time:         binary.LittleEndian.Uint32(notify.Data[3:7]),
				SampleNum:    binary.LittleEndian.Uint16(notify.Data[7:9]),
				Temperature:  math.Float32frombits(binary.LittleEndian.Uint32(notify.Data[9:12])),
				Humidity:     math.Float32frombits(binary.LittleEndian.Uint32(notify.Data[12:16])),
			}

			log.Infow("温湿度センサデータ", "温度", th.Temperature, "湿度", th.Humidity, "電池残量", th.BatteryLevel, "サンプリング間隔", th.Sampling, "サンプル数", th.SampleNum, "センサ時刻", time.Unix(int64(th.Time), 0).UTC())
		}
	}
}

func handleDownlinkResponse(c *serial.Com, f serial.Frame) {

	log := myctx.MustLogger(c.Context())

	resp, ok := f.(*serial.DownlinkResponse)
	if !ok {
		log.Errorw("Invalid frame type")
		return
	}

	log.Infow("DownlinkResponse 受信", "result", resp.Result)
}

func handleInfoResponse(c *serial.Com, f serial.Frame) {

	log := myctx.MustLogger(c.Context())

	resp, ok := f.(*serial.InfoResponse)
	if !ok {
		log.Errorw("Invalid frame type")
		return
	}

	log.Infow("InfoResponse 受信", "command", resp.Command)
}

func handleDFUResponse(c *serial.Com, f serial.Frame) {

	log := myctx.MustLogger(c.Context())

	resp, ok := f.(*serial.DFUResponse)
	if !ok {
		log.Errorw("Invalid frame type")
		return
	}

	log.Infow("DFUResponse 受信", "result", resp.Result)
}

func handleErrorNotify(c *serial.Com, f serial.Frame) {

	log := myctx.MustLogger(c.Context())

	errNotify, ok := f.(*serial.ErrorNotify)
	if !ok {
		log.Errorw("Invalid frame type")
		return
	}

	log.Infow("ErrorNotify 受信", "code", errNotify.Reason)

	if errNotify.Reason == serial.ReasonKeepAliveRequired {

		log.Infow("LocalTime", "time", time.Now().Local().Unix())
		log.Infow("UnixTime", "time", time.Now().Unix())

		ir := serial.InfoRequest{
			ProtocolVersion: 0x01,
			Type:            0x01,
			Command:         serial.CommandKeepAlive,
			LocalTime:       uint32(time.Now().Local().Unix()),
			UnixTime:        uint32(time.Now().Unix()),
		}

		c.Write(&ir)
	}
}
