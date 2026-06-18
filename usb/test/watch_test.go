package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tuken/triton/usb"
)

func main() {

	watch := usb.NewWatch(context.Background())

	port := watch.Find("BraveJIG Router")
	if port != "" {
		fmt.Println("既に接続されている:", port)
	}

	watch.Start(time.Second*3, "BraveJIG Router")
	defer watch.Stop()

	// Ctrl-C / SIGTERM で停止できるようにする
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

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

	time.Sleep(time.Second * 100)
}
