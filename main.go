package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"sync"
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
	var comMu sync.RWMutex

	// connect はポートを開き、Run を goroutine で開始する。
	connect := func(portName string) {

		comMu.RLock()
		connected := com != nil
		comMu.RUnlock()

		if connected {
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

		comMu.Lock()
		com = c
		runDone = make(chan struct{})
		rd := runDone
		comMu.Unlock()

		// Run はブロックするので goroutine で回す。抜去/切断で終了する。
		go func() {
			defer close(rd)

			err := c.Run()
			if err != nil && !errors.Is(err, context.Canceled) {
				log.Errorw("Run 異常終了", "error", err)

				// 異常終了時、ポートが開いたままだと再接続に失敗し続ける可能性があるため、
				// 明示的に切断してリカバリ可能な状態へ戻す。
				if derr := c.Disconnect(); derr != nil {
					log.Warnw("Run 異常終了後の切断エラー", "error", derr)
				}
			}

			// この goroutine が現在のアクティブ接続なら、状態を未接続へ戻す。
			comMu.Lock()
			if com == c {
				com = nil
				runDone = nil
			}
			comMu.Unlock()
		}()
	}

	// disconnect はポートを閉じ、Run goroutine の終了を待つ。
	disconnect := func() {

		comMu.Lock()
		c := com
		rd := runDone
		com = nil
		runDone = nil
		comMu.Unlock()

		if c == nil {
			return
		}

		if err := c.Disconnect(); err != nil {
			log.Warnw("切断エラー", "error", err)
		}

		if rd != nil {
			<-rd // Run goroutine が抜けるのを待ってから片付ける
		}

		log.Infow("シリアル切断")
	}

	// 既接続・新規挿入はどちらも EventInserted で届くので、ここでは待つだけ。
	log.Infow("USB挿入待機中")

	// 接続失敗時、挿抜イベントが来なくても一定間隔で再試行する。
	// （USB が挿さったまま一時的に open 失敗するケース向け）
	var retryPort string
	retryTicker := time.NewTicker(3 * time.Second)
	defer retryTicker.Stop()

	susp := make(chan os.Signal, 1)
	signal.Notify(susp, syscall.SIGTSTP, syscall.SIGCONT, syscall.SIGUSR1, syscall.SIGUSR2)

	go func() {

		sendIfConnected := func(name string, req serial.Requestable) {
			comMu.RLock()
			c := com
			comMu.RUnlock()

			if c == nil {
				log.Warnw("シリアル未接続のため送信をスキップ", "signal", name)
				return
			}

			if err := c.Write(req); err != nil {
				log.Warnw("シグナル送信コマンド失敗", "signal", name, "error", err)
			}
		}

		for sig := range susp {

			switch sig {

			case syscall.SIGTSTP:
				log.Infow("一時停止!!!")
				sendIfConnected("SIGTSTP", packet.NewStopRequest())

			case syscall.SIGCONT:
				log.Infow("再開!!!")
				sendIfConnected("SIGCONT", packet.NewStartRequest())

			case syscall.SIGUSR1:
				log.Infow("KeepAlive!!!")
				sendIfConnected("SIGUSR1", packet.NewKeepAliveRequest())

			case syscall.SIGUSR2:
				log.Infow("FWバージョン取得!!!")
				sendIfConnected("SIGUSR2", packet.NewGetVersionRequest())
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

		case <-retryTicker.C:
			if retryPort != "" {
				connect(retryPort)
			}

		case ev, ok := <-watch.Events():

			if !ok {
				disconnect()
				return
			}

			switch ev.Kind {

			case usb.EventInserted:
				log.Infow("USB挿入検知", "port", ev.PortName)
				retryPort = ev.PortName
				connect(ev.PortName)

			case usb.EventRemoved:
				log.Infow("USB抜去検知", "port", ev.PortName)
				if retryPort == ev.PortName {
					retryPort = ""
				}
				disconnect()
			}
		}
	}
}

func handleUplinkNotify(c *serial.Com, p serial.Responder) {

	log := myctx.MustLogger(c.Context())

	notify, ok := p.(*packet.UplinkNotify)
	if !ok {
		log.Errorw("Invalid frame type")
		return
	}

	log.Infow("UplinkNotify 受信", "data", notify)
}

func handleDownlinkResponse(c *serial.Com, p serial.Responder) {

	log := myctx.MustLogger(c.Context())

	resp, ok := p.(*packet.DownlinkResponse)
	if !ok {
		log.Errorw("Invalid frame type")
		return
	}

	log.Infow("DownlinkResponse 受信", "result", resp)
}

func handleJIGInfoResponse(c *serial.Com, p serial.Responder) {

	log := myctx.MustLogger(c.Context())

	resp, ok := p.(*packet.JIGInfoResponse)
	if !ok {
		log.Errorw("Invalid frame type")
		return
	}

	log.Infow("JIGInfoResponse 受信", "result", resp)
}

func handleDFUResponse(c *serial.Com, p serial.Responder) {

	log := myctx.MustLogger(c.Context())

	resp, ok := p.(*packet.DFUResponse)
	if !ok {
		log.Errorw("Invalid frame type")
		return
	}

	log.Infow("DFUResponse 受信", "result", resp.Result)
}

func handleErrorNotify(c *serial.Com, p serial.Responder) {

	log := myctx.MustLogger(c.Context())

	err, ok := p.(*packet.ErrorNotify)
	if !ok {
		log.Errorw("Invalid frame type")
		return
	}

	log.Infow("エラー通知", "data", err)

	// if errNotify.Reason == serial.ReasonKeepAliveRequired {

	// 	log.Infow("KeepAlive要求", "unix time", time.Now().Unix(), "local time", time.Now().Local().Unix())

	// 	c.Write(packet.NewKeepAliveRequest())
	// }
}
