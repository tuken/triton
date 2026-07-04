package main

import (
	"context"
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

	log.Infow("UplinkNotify 受信", "data length", notify.DataLength, "unix time", time.Unix(int64(notify.UnixTime), 0).UTC(), "device id", notify.DeviceID, "sensor id", notify.SensorID, "rssi", notify.Rssi, "sequence no", notify.SequenceNo)
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
