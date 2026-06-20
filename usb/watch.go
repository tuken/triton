package usb

import (
	"context"
	"strings"
	"sync"
	"time"

	myctx "github.com/tuken/triton/context"
	"go.bug.st/serial/enumerator"
)

type Watch struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	events chan Event
}

// EventKind はUSBイベントの種別を表す。
type EventKind int

const (
	EventInserted EventKind = iota // 挿入
	EventRemoved                   // 抜去
)

// Event は検知したUSBイベントを表す。
type Event struct {
	Kind     EventKind
	PortName string
}

func NewWatch(ctx context.Context) *Watch {

	// Stop() から ctx.Done() を発火させるためキャンセル可能にする
	ctx, cancel := context.WithCancel(ctx)

	return &Watch{
		ctx:    ctx,
		cancel: cancel,
		// バッファを持たせ、受信側が一時的に遅くても watch ループを止めない
		events: make(chan Event, 16),
	}
}

// Events 検知したイベントを受け取る受信専用チャネルを返す。Stop()後にクローズされるのでrangeで受信できる。
func (w *Watch) Events() <-chan Event {

	return w.events
}

func (w *Watch) Start(interval time.Duration, target string) {

	log := myctx.MustLogger(w.ctx)

	log.Infow("USB監視開始", "interval", interval)

	w.wg.Add(1)

	go func() {
		defer w.wg.Done()
		w.watch(interval, target)
	}()
}

// Stop は監視を停止し、goroutineが終了するまで待つ。
func (w *Watch) Stop() {

	log := myctx.MustLogger(w.ctx)

	w.cancel()

	w.wg.Wait()

	// watch goroutine が終了した後に閉じる。（送信側が終わってから閉じるので panic しない）
	close(w.events)

	log.Infow("USB監視終了")
}

func (w *Watch) Find(target string) string {

	// 直前のスナップショット。キー: 一意キー / 値: ポート情報
	prev := w.scan(target)

	// 起動時点で既に挿さっているものを通知
	for _, p := range prev {

		return p.Name
	}

	return ""
}

// emit イベントを通知する。受信側が詰まっても watch ループを止めないよう、ctx.Done()でも抜けられるselectで送信する。
func (w *Watch) emit(kind EventKind, p *enumerator.PortDetails) {

	select {
	case w.events <- Event{Kind: kind, PortName: p.Name}:
	case <-w.ctx.Done():
	}
}

// watch intervalごとにシリアルポート一覧を取得し、Productがtargetに合致するポートの挿入・抜去を検知してログ出力する。
func (w *Watch) watch(interval time.Duration, target string) {

	// log := myctx.MustLogger(w.ctx)

	// 直前のスナップショット。キー: 一意キー / 値: ポート情報
	prev := w.scan(target)

	// // 起動時点で既に挿さっているものを通知
	// for _, p := range prev {
	// 	log.Infow("検出(起動時に接続済み)", "port", p.Name, "vid", p.VID, "pid", p.PID, "serial", p.SerialNumber, "config", p.Configuration, "manufacturer", p.Manufacturer, "product", p.Product)
	// }

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {

		select {

		case <-w.ctx.Done():
			return

		case <-ticker.C:
			cur := w.scan(target)

			// 追加されたもの
			for key, p := range cur {

				if _, ok := prev[key]; !ok {
					// log.Infow("USB挿入を検知", "port", p.Name, "vid", p.VID, "pid", p.PID, "serial", p.SerialNumber, "config", p.Configuration, "manufacturer", p.Manufacturer, "product", p.Product)
					w.emit(EventInserted, p)
				}
			}

			// 削除されたもの
			for key, p := range prev {

				if _, ok := cur[key]; !ok {
					// log.Infow("USB抜去を検知", "port", p.Name, "vid", p.VID, "pid", p.PID, "serial", p.SerialNumber, "config", p.Configuration, "manufacturer", p.Manufacturer, "product", p.Product)
					w.emit(EventRemoved, p)
				}
			}

			prev = cur
		}
	}
}

// scan はシリアルポート一覧を取得し、match に合致するものを一意キーで返す。
func (w *Watch) scan(target string) map[string]*enumerator.PortDetails {

	result := map[string]*enumerator.PortDetails{}

	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {

		log := myctx.MustLogger(w.ctx)
		log.Warnw("enumerator.GetDetailedPortsListエラー", "error", err)
		return result
	}

	for _, p := range ports {

		if !p.IsUSB {
			continue
		}

		if !strings.Contains(p.Product, target) {
			continue
		}

		result["usb:"+p.VID+":"+p.PID+":"+p.SerialNumber] = p
	}

	return result
}
