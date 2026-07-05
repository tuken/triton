package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	myctx "github.com/tuken/triton/context"
	"github.com/tuken/triton/logger"
	"github.com/tuken/triton/usb"
)

func main() {

	log := logger.NewLogger()
	ctx := context.WithValue(context.Background(), myctx.LoggerKey, log)

	// Ctrl-C / SIGTERM が来ると ctx がキャンセルされる
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	watch := usb.NewWatch(ctx, "BraveJIG Router", time.Second*3)
	watch.Start()
	defer watch.Stop()

	// 受信goroutineを先に起動しておく（シグナル待ちより前に動かす）
	go func() {

		for ev := range watch.Events() {

			switch ev.Kind {

			case usb.EventInserted:
				fmt.Println("挿入された:", ev.PortName)
				// ここで別処理（シリアルオープン等）を起動
			case usb.EventRemoved:
				fmt.Println("抜去された:", ev.PortName)
			}
		}
		// Stop() で close されると、このループを抜ける
	}()

	// Ctrl-C / SIGTERM が来るまで待ち、来たら main を抜けて defer watch.Stop() が走る
	<-ctx.Done()
}
