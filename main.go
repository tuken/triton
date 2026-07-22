package main

import (
	"context"
	"errors"
	"fmt"
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
		c.Handle(serial.TypeJIGInfoResponse, handleJIGInfoResponse)
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

	susp := make(chan os.Signal, 1)
	signal.Notify(susp, syscall.SIGTSTP, syscall.SIGCONT, syscall.SIGUSR1)

	go func() {

		for sig := range susp {

			switch sig {

			case syscall.SIGTSTP:
				log.Infow("一時停止!!!")

				signal.Reset(syscall.SIGTSTP)
				com.Write(packet.NewStopRequest())

			case syscall.SIGCONT:
				log.Infow("再開!!!")

				signal.Notify(susp, syscall.SIGTSTP)
				com.Write(packet.NewStartRequest())

			case syscall.SIGUSR1:
				log.Infow("KeepAlive!!!")

				com.Write(packet.NewKeepAliveRequest())
			}
		}
	}()

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

func handleJIGInfoResponse(c *serial.Com, p serial.Packet) {

	log := myctx.MustLogger(c.Context())

	resp, ok := p.(*packet.JIGInfoResponse)
	if !ok {
		log.Errorw("Invalid frame type")
		return
	}

	log.Infow("JIG Infoレスポンス", "packet", resp)
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

	err, ok := p.(*packet.ErrorNotify)
	if !ok {
		log.Errorw("Invalid frame type")
		return
	}

	log.Infow("エラー通知", "packet", err)

	// if errNotify.Reason == serial.ReasonKeepAliveRequired {

	// 	log.Infow("KeepAlive要求", "unix time", time.Now().Unix(), "local time", time.Now().Local().Unix())

	// 	c.Write(packet.NewKeepAliveRequest())
	// }
}
