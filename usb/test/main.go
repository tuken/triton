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

	watch := usb.NewWatch(ctx)

	port := watch.Find("BraveJIG Router")
	if port != "" {
		fmt.Println("既に接続されている:", port)
	}

	watch.Start(time.Second*3, "BraveJIG Router")
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

	// Ctrl-C / SIGTERM が来るまでここで待ち、来たら main を抜けて defer watch.Stop() が走る
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
}
