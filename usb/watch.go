package usb

import (
	"context"
	"strings"
	"sync"
	"time"

	myctx "github.com/tuken/triton/context"
	"go.bug.st/serial/enumerator"
)

// EventKind はUSBイベントの種別を表す。
type EventKind int

const (
	EventInserted EventKind = iota // 挿入（起動時に既に挿さっていた場合も含む）
	EventRemoved                   // 抜去
)

func (k EventKind) String() string {

	switch k {

	case EventInserted:
		return "inserted"

	case EventRemoved:
		return "removed"

	default:
		return "unknown"
	}
}

// Event は検知したUSBイベントを表す。
type Event struct {
	Kind     EventKind
	PortName string
}

// Watch は target（製品名の部分一致）に合致するUSBシリアルポートの
// 挿入・抜去を、一定間隔のポーリングで監視する。
//
// 使い方:
//
//	w := usb.NewWatch(ctx, "BraveJIG Router", time.Second)
//	w.Start()
//	defer w.Stop()
//	for ev := range w.Events() {
//	    // ev.Kind と ev.PortName で処理を分岐
//	}
//
// 起動時に既に挿さっているポートも EventInserted として通知されるため、
// 「起動時に接続済みか」を別途調べる必要はない。
type Watch struct {
	ctx      context.Context
	cancel   context.CancelFunc
	target   string
	interval time.Duration
	events   chan Event
	wg       sync.WaitGroup
}

// NewWatch は target を監視する Watch を生成する。
// target はポートの Product 名に対する部分一致で判定する。
func NewWatch(ctx context.Context, target string, interval time.Duration) *Watch {

	// Stop() から監視ループを止められるようキャンセル可能にする
	ctx, cancel := context.WithCancel(ctx)

	return &Watch{
		ctx:      ctx,
		cancel:   cancel,
		target:   target,
		interval: interval,
		// バッファを持たせ、受信側が一時的に遅くても監視ループを止めない
		events: make(chan Event, 16),
	}
}

// Events は挿入・抜去イベントを受け取る受信専用チャネルを返す。
// Stop() 後にクローズされるので range で受信できる。
func (w *Watch) Events() <-chan Event {

	return w.events
}

// Start は監視を開始する。開始直後に一度走査し、既に挿さっているポートを
// EventInserted として通知する。以降は interval ごとに差分を通知する。
func (w *Watch) Start() {

	log := myctx.MustLogger(w.ctx)
	log.Infow("USB監視開始", "target", w.target, "interval", w.interval)

	w.wg.Add(1)

	go func() {
		defer w.wg.Done()
		w.loop()
	}()
}

// Stop は監視を停止し、goroutine の終了を待ってからイベントチャネルを閉じる。
func (w *Watch) Stop() {

	w.cancel()
	w.wg.Wait()

	// 監視ループ（送信側）が終わってから閉じるので panic しない
	close(w.events)

	myctx.MustLogger(w.ctx).Infow("USB監視終了")
}

// loop は interval ごとにポートを走査し、前回との差分を挿抜イベントとして通知する。
func (w *Watch) loop() {

	// known: 現在挿さっていると認識しているポート集合（一意キー → ポート名）。
	// 空から始めることで、初回走査で見つかったポートは「挿入」として通知される。
	known := map[string]string{}

	// 起動直後に1回走査して既接続を即通知する
	w.poll(known)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {

		select {

		case <-w.ctx.Done():
			return

		case <-ticker.C:
			w.poll(known)
		}
	}
}

// poll は1回分の走査を行い、known を更新しながら差分をイベント通知する。
func (w *Watch) poll(known map[string]string) {

	current := w.scan()

	// known に無い → 新しく挿入された
	for key, name := range current {

		if _, ok := known[key]; !ok {
			known[key] = name
			w.emit(EventInserted, name)
		}
	}

	// current に無い → 抜去された
	for key, name := range known {

		if _, ok := current[key]; !ok {
			delete(known, key)
			w.emit(EventRemoved, name)
		}
	}
}

// scan は target を含むUSBシリアルポートを「一意キー → ポート名」で返す。
func (w *Watch) scan() map[string]string {

	result := map[string]string{}

	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		myctx.MustLogger(w.ctx).Warnw("ポート一覧取得エラー", "error", err)
		return result
	}

	for _, p := range ports {

		if !p.IsUSB || !strings.Contains(p.Product, w.target) {
			continue
		}

		key := "usb:" + p.VID + ":" + p.PID + ":" + p.SerialNumber
		result[key] = p.Name
	}

	return result
}

// emit はイベントを通知する。受信側が詰まっても、ctx キャンセルで抜けられる。
func (w *Watch) emit(kind EventKind, portName string) {

	select {

	case w.events <- Event{Kind: kind, PortName: portName}:

	case <-w.ctx.Done():
	}
}
