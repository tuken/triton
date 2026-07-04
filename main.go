package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	myctx "github.com/tuken/triton/context"
	"github.com/tuken/triton/logger"
	"github.com/tuken/triton/router"
	"github.com/tuken/triton/usb"
	"go.bug.st/serial"
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

		fmt.Println("未接続:", portName)
		return
	}

	log.Infow("アプリケーション起動")

	com := router.NewCom()

	// COMポートをオープン
	if err := com.Connect(portName, &serial.Mode{
		BaudRate: 115200,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	}); err != nil {
		log.Fatalw("USB接続エラー", "error", err)
		return
	}

	defer com.Disconnect()

	com.Run(ctx)
}
