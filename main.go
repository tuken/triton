package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"os/signal"
	"syscall"
	"time"

	myctx "github.com/tuken/triton/context"
	"github.com/tuken/triton/logger"
	"github.com/tuken/triton/serial"
	"github.com/tuken/triton/serial/packet"
	"github.com/tuken/triton/usb"
	goser "go.bug.st/serial"
)

func main() {

	// Ctrl-C / SIGTERM が来ると ctx がキャンセルされる
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log := logger.NewLogger()
	ctx = context.WithValue(ctx, myctx.LoggerKey, log)

	const target = "BraveJIG Router"

	mode := &goser.Mode{
		BaudRate: 115200,
		DataBits: 8,
		Parity:   goser.NoParity,
		StopBits: goser.OneStopBit,
	}

	log.Infow("アプリケーション起動")

	// USB のホットプラグ監視を開始（挿入・抜去イベントを受け取る）。
	// 起動時に既に挿さっているポートも EventInserted として届く。
	watch := usb.NewWatch(ctx, target, 1*time.Second)
	watch.Start()
	defer watch.Stop()

	// 現在の接続。未接続なら com == nil。runDone は Run goroutine の終了通知。
	var com *serial.Com
	var runDone chan struct{}

	// connect はポートを開き、Run を goroutine で開始する。
	connect := func(portName string) {

		if com != nil {
			return // 既に接続済み
		}

		c := serial.NewCom(ctx)

		c.Handle(serial.TypeUplinkNotify, handleUplinkNotify)
		c.Handle(serial.TypeDownlinkResponse, handleDownlinkResponse)
		c.Handle(serial.TypeInfoResponse, handleInfoResponse)
		c.Handle(serial.TypeDFUResponse, handleDFUResponse)
		c.Handle(serial.TypeErrorNotify, handleErrorNotify)

		if err := c.Connect(portName, mode); err != nil {
			log.Errorw("USB接続エラー", "port", portName, "error", err)
			return
		}

		log.Infow("シリアル接続", "port", portName)

		com = c
		runDone = make(chan struct{})

		// Run はブロックするので goroutine で回す。抜去/切断で終了する。
		go func() {
			defer close(runDone)

			if err := c.Run(); err != nil && !errors.Is(err, context.Canceled) {
				log.Errorw("Run 終了", "error", err)
			}
		}()
	}

	// disconnect はポートを閉じ、Run goroutine の終了を待つ。
	disconnect := func() {

		if com == nil {
			return
		}

		if err := com.Disconnect(); err != nil {
			log.Warnw("切断エラー", "error", err)
		}

		<-runDone // Run goroutine が抜けるのを待ってから片付ける

		log.Infow("シリアル切断")

		com = nil
		runDone = nil
	}

	// 既接続・新規挿入はどちらも EventInserted で届くので、ここでは待つだけ。
	log.Infow("USB挿入待機中")

	// イベントループ：挿入で接続、抜去で切断。ctx キャンセルで終了。
	for {

		select {

		case <-ctx.Done():
			disconnect()
			log.Infow("アプリケーション終了")
			return

		case ev, ok := <-watch.Events():

			if !ok {
				disconnect()
				return
			}

			switch ev.Kind {

			case usb.EventInserted:
				log.Infow("USB挿入検知", "port", ev.PortName)
				connect(ev.PortName)

			case usb.EventRemoved:
				log.Infow("USB抜去検知", "port", ev.PortName)
				disconnect()
			}
		}
	}
}

func handleUplinkNotify(c *serial.Com, p serial.Packet) {

	log := myctx.MustLogger(c.Context())

	notify, ok := p.(*packet.UplinkNotify)
	if !ok {
		log.Errorw("Invalid frame type")
		return
	}

	log.Infow("UplinkNotify 受信", "data length", notify.DataLength, "unix time", time.Unix(int64(notify.UnixTime), 0).UTC(), "device id", fmt.Sprintf("%016x", notify.DeviceID), "sensor id", fmt.Sprintf("%04x", notify.SensorID), "rssi", notify.Rssi, "sequence no", fmt.Sprintf("%04x", notify.SequenceNo))

	if notify.SensorID == 0x0123 {

		type Thermo struct {
			BatteryLevel byte    `json:"battery_level"`
			Sampling     byte    `json:"sampling"`
			Time         uint32  `json:"time"`
			SampleNum    uint16  `json:"sample_num"`
			Temperature  float32 `json:"temperature"`
			Humidity     float32 `json:"humidity"`
		}

		if len(notify.Data) >= 16 {

			th := Thermo{
				BatteryLevel: notify.Data[0],
				Sampling:     notify.Data[1],
				Time:         binary.LittleEndian.Uint32(notify.Data[2:6]),
				SampleNum:    binary.LittleEndian.Uint16(notify.Data[6:8]),
				Temperature:  math.Float32frombits(binary.LittleEndian.Uint32(notify.Data[8:12])),
				Humidity:     math.Float32frombits(binary.LittleEndian.Uint32(notify.Data[12:16])),
			}

			log.Infow("温湿度センサデータ", "温度", th.Temperature, "湿度", th.Humidity, "電池残量", th.BatteryLevel, "サンプリング間隔", th.Sampling, "サンプル数", th.SampleNum, "センサ時刻", time.Unix(int64(th.Time), 0).UTC())
		}
	}
}

func handleDownlinkResponse(c *serial.Com, p serial.Packet) {

	log := myctx.MustLogger(c.Context())

	resp, ok := p.(*serial.DownlinkResponse)
	if !ok {
		log.Errorw("Invalid frame type")
		return
	}

	log.Infow("DownlinkResponse 受信", "result", resp.Result)
}

func handleInfoResponse(c *serial.Com, p serial.Packet) {

	log := myctx.MustLogger(c.Context())

	resp, ok := p.(*serial.InfoResponse)
	if !ok {
		log.Errorw("Invalid frame type")
		return
	}

	log.Infow("InfoResponse 受信", "command", resp.Command)
}

func handleDFUResponse(c *serial.Com, p serial.Packet) {

	log := myctx.MustLogger(c.Context())

	resp, ok := p.(*serial.DFUResponse)
	if !ok {
		log.Errorw("Invalid frame type")
		return
	}

	log.Infow("DFUResponse 受信", "result", resp.Result)
}

func handleErrorNotify(c *serial.Com, p serial.Packet) {

	log := myctx.MustLogger(c.Context())

	errNotify, ok := p.(*serial.ErrorNotify)
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
