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

	com.Handle(router.TypeUplinkNotify, handleUplinkNotify)
	com.Handle(router.TypeDownlinkResponse, handleDownlinkResponse)
	com.Handle(router.TypeInfoResponse, handleInfoResponse)
	com.Handle(router.TypeDFUResponse, handleDFUResponse)
	com.Handle(router.TypeErrorNotify, handleErrorNotify)

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

func handleUplinkNotify(c *router.Com, f router.Frame) {

	notify, ok := f.(*router.UplinkNotify)
	if !ok {
		panic("invalid frame type")
	}

	fmt.Println("UplinkNotify 受信", "data", notify.Data)
}

func handleDownlinkResponse(c *router.Com, f router.Frame) {

	resp, ok := f.(*router.DownlinkResponse)
	if !ok {
		panic("invalid frame type")
	}

	fmt.Println("DownlinkResponse 受信", "result", resp.Result)
}

func handleInfoResponse(c *router.Com, f router.Frame) {

	resp, ok := f.(*router.InfoResponse)
	if !ok {
		panic("invalid frame type")
	}

	fmt.Println("InfoResponse 受信", "command", resp.Command)
}

func handleDFUResponse(c *router.Com, f router.Frame) {
	resp, ok := f.(*router.DFUResponse)
	if !ok {
		panic("invalid frame type")
	}

	fmt.Println("DFUResponse 受信", "result", resp.Result)
}

func handleErrorNotify(c *router.Com, f router.Frame) {

	errNotify, ok := f.(*router.ErrorNotify)
	if !ok {
		panic("invalid frame type")
	}

	fmt.Println("ErrorNotify 受信", "code", errNotify.Reason)

	if errNotify.Reason == router.ReasonKeepAliveRequired {

		fmt.Println("LocalTime:", time.Now().Local().Unix())
		fmt.Println("UnixTime:", time.Now().Unix())

		ir := router.InfoRequest{
			ProtocolVersion: 0x01,
			Type:            0x01,
			Command:         router.CommandKeepAlive,
			LocalTime:       uint32(time.Now().Local().Unix()),
			UnixTime:        uint32(time.Now().Unix()),
		}

		c.Write(&ir)
	}
}
