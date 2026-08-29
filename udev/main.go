//go:build linux

package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/pilebones/go-udev/netlink"
)

func main() {

	conn := new(netlink.UEventConn)

	if err := conn.Connect(netlink.UdevEvent); err != nil {
		log.Fatal("netlink uevent の購読に失敗: ", err)
	}
	defer conn.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	events := make(chan netlink.UEvent)
	errs := make(chan error)

	// 第4引数はフィルタ。nil は全イベントを受信する。
	go conn.MonitorWithContext(ctx, events, errs, nil)

	for {

		select {

		case event, ok := <-events:
			if !ok {
				return
			}

			// USBデバイス本体だけに絞る。
			if event.Env["SUBSYSTEM"] != "usb" || event.Env["DEVTYPE"] != "usb_device" {
				continue
			}

			switch event.Action {

			case "add":
				log.Printf("USB挿入: path=%s product=%s", event.KObj, event.Env["PRODUCT"])

			case "remove":
				log.Printf("USB抜去: path=%s product=%s", event.KObj, event.Env["PRODUCT"])
			}

		case err := <-errs:
			// 読み取りエラーは監視終了を必ずしも意味しない。
			if err != nil && !errors.Is(err, netlink.ErrUntrustedSender) {
				log.Printf("udev monitor error: %v", err)
			}

		case <-ctx.Done():
			return
		}
	}
}
