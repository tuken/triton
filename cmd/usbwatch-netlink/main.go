//go:build linux

package main

import (
	"bufio"
	"context"
	"flag"
	"os"
	"os/signal"
	"strings"
	"syscall"

	myctx "github.com/tuken/triton/context"
	"github.com/tuken/triton/logger"
	"golang.org/x/sys/unix"
)

func main() {

	// DEVNAME（例: ttyACM0）に含まれる文字列でフィルタする。空なら全 tty 対象。
	match := flag.String("match", "ttyACM", "対象とする DEVNAME に含まれる文字列(空で全件)")
	flag.Parse()

	log := logger.NewLogger()
	ctx := context.WithValue(context.Background(), myctx.LoggerKey, log)

	// Ctrl-C / SIGTERM で停止
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Infow("USBリアルタイム監視開始(netlink)", "match", *match)

	if err := watch(ctx, *match); err != nil {
		log.Fatalw("監視エラー", "error", err)
		return
	}

	log.Infow("USB監視終了")
}

// uevent は udev/カーネルから届く 1 イベントを表す。
type uevent struct {
	Action  string            // add, remove, change ...
	DevName string            // ttyACM0 など
	DevPath string            // /devices/... (sysfs 相対)
	Subsys  string            // tty, usb, block ...
	Env     map[string]string // その他すべてのプロパティ
}

// watch は netlink(uevent) を購読し、tty サブシステムの add/remove を即時検知する。
func watch(ctx context.Context, match string) error {

	log := myctx.MustLogger(ctx)

	// NETLINK_KOBJECT_UEVENT を購読するソケットを開く
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_KOBJECT_UEVENT)
	if err != nil {
		return err
	}
	defer unix.Close(fd)

	// Groups=1 でカーネルのブロードキャストグループを購読
	addr := &unix.SockaddrNetlink{
		Family: unix.AF_NETLINK,
		Groups: 1,
		Pid:    uint32(os.Getpid()),
	}
	if err := unix.Bind(fd, addr); err != nil {
		return err
	}

	// 受信タイムアウトを設定し、Recvfrom を定期的にタイムアウトさせる。
	// これにより、イベント待機中でも ctx のキャンセルを検知して終了できる。
	// （netlink ソケットでは Shutdown/Close が待機中の Recvfrom を確実に
	//   解除できない環境があるため、ポーリング方式にしている）
	tv := unix.Timeval{Sec: 1, Usec: 0}
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
		return err
	}

	buf := make([]byte, 8192)

	for {
		// ループ先頭で ctx をチェックして即時終了に対応
		if ctx.Err() != nil {
			return nil
		}

		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			// ctx 終了なら正常終了扱い
			if ctx.Err() != nil {
				return nil
			}
			// 受信タイムアウト/割り込みはループを継続して ctx を再チェック
			if err == unix.EAGAIN || err == unix.EWOULDBLOCK || err == unix.EINTR {
				continue
			}
			return err
		}

		ev := parseUevent(buf[:n])
		if ev == nil {
			continue
		}

		// tty サブシステムのみ対象
		if ev.Subsys != "tty" {
			continue
		}

		// DEVNAME によるフィルタ
		if match != "" && !strings.Contains(ev.DevName, match) {
			continue
		}

		switch ev.Action {
		case "add":
			log.Infow("USB挿入を検知", "device", "/dev/"+ev.DevName, "devpath", ev.DevPath)
		case "remove":
			log.Infow("USB抜去を検知", "device", "/dev/"+ev.DevName, "devpath", ev.DevPath)
		default:
			log.Debugw("その他イベント", "action", ev.Action, "device", ev.DevName)
		}
	}
}

// parseUevent はカーネル uevent メッセージをパースする。
// 形式: "add@/devices/...\0ACTION=add\0DEVNAME=ttyACM0\0SUBSYSTEM=tty\0..."
// （udev 形式の "libudev\0" ヘッダ付きメッセージは無視する）
func parseUevent(data []byte) *uevent {

	// udev が転送するメッセージは "libudev" マジックで始まる。
	// カーネル直送(kernel)だけを扱いたいのでこれは除外する。
	if strings.HasPrefix(string(data), "libudev") {
		return nil
	}

	ev := &uevent{Env: map[string]string{}}

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Split(splitNull)

	first := true
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// 先頭行は "add@/devices/..." の形式（KEY=VALUE ではない）
		if first {
			first = false
			if !strings.Contains(line, "=") {
				continue
			}
		}

		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		switch key {
		case "ACTION":
			ev.Action = val
		case "DEVNAME":
			ev.DevName = val
		case "DEVPATH":
			ev.DevPath = val
		case "SUBSYSTEM":
			ev.Subsys = val
		}
		ev.Env[key] = val
	}

	if ev.Action == "" {
		return nil
	}
	return ev
}

// splitNull は NUL 区切りで分割する bufio.SplitFunc。
func splitNull(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i := 0; i < len(data); i++ {
		if data[i] == 0 {
			return i + 1, data[:i], nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}
