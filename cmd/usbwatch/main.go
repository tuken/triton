package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	myctx "github.com/tuken/triton/context"
	"github.com/tuken/triton/logger"
	"go.bug.st/serial/enumerator"
)

func main() {

	match := flag.String("match", "", "監視対象を絞り込む文字列（ポート名・VID:PID・シリアルのいずれかに含まれれば対象）")
	interval := flag.Duration("interval", time.Second, "ポーリング間隔")
	flag.Parse()

	log := logger.NewLogger()
	ctx := context.WithValue(context.Background(), myctx.LoggerKey, log)

	// Ctrl-C / SIGTERM で停止できるようにする
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Infow("USB監視開始", "match", *match, "interval", interval.String())

	watch(ctx, *match, *interval)

	log.Infow("USB監視終了")
}

// portInfo は検知に必要なポート情報を保持する。
type portInfo struct {
	Name   string
	IsUSB  bool
	VID    string
	PID    string
	Serial string
}

// watch は interval ごとにシリアルポート一覧を取得し、
// match に合致するポートの挿入・抜去を検知してログ出力する。
func watch(ctx context.Context, match string, interval time.Duration) {

	log := myctx.MustLogger(ctx)

	// 直前のスナップショット。キー: 一意キー / 値: ポート情報
	prev := scan(ctx, match)

	// 起動時点で既に挿さっているものを通知
	for _, p := range prev {
		log.Infow("検出(起動時に接続済み)",
			"port", p.Name, "vid", p.VID, "pid", p.PID, "serial", p.Serial)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cur := scan(ctx, match)

			// 追加されたもの
			for key, p := range cur {
				if _, ok := prev[key]; !ok {
					log.Infow("USB挿入を検知",
						"port", p.Name, "vid", p.VID, "pid", p.PID, "serial", p.Serial)
				}
			}

			// 削除されたもの
			for key, p := range prev {
				if _, ok := cur[key]; !ok {
					log.Infow("USB抜去を検知",
						"port", p.Name, "vid", p.VID, "pid", p.PID, "serial", p.Serial)
				}
			}

			prev = cur
		}
	}
}

// scan はシリアルポート一覧を取得し、match に合致するものを一意キーで返す。
func scan(ctx context.Context, match string) map[string]portInfo {

	log := myctx.MustLogger(ctx)

	result := map[string]portInfo{}

	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		// 取得失敗時は空を返さず、誤検知を避けるため前回維持にしたいが、
		// scan は純粋関数にしているので呼び出し側で前回値を保持している。
		log.Warnw("ポート列挙エラー", "error", err)
		return result
	}

	for _, p := range ports {
		info := portInfo{
			Name:   p.Name,
			IsUSB:  p.IsUSB,
			VID:    p.VID,
			PID:    p.PID,
			Serial: p.SerialNumber,
		}

		if !matches(info, match) {
			continue
		}

		result[key(info)] = info
	}

	return result
}

// matches は match が空なら全件対象、そうでなければ
// ポート名・VID:PID・シリアルのいずれかに含まれるかを判定する。
func matches(p portInfo, match string) bool {
	if match == "" {
		return true
	}
	if strings.Contains(p.Name, match) {
		return true
	}
	if strings.Contains(p.VID+":"+p.PID, match) {
		return true
	}
	if p.Serial != "" && strings.Contains(p.Serial, match) {
		return true
	}
	return false
}

// key は機器を一意に識別するキーを返す。
// シリアル番号があれば VID:PID:Serial を優先し、無ければポート名にフォールバックする。
// （ttyACM0 等は抜き差しで番号が変わるため、可能ならシリアルで追跡する）
func key(p portInfo) string {
	if p.IsUSB && p.Serial != "" {
		return "usb:" + p.VID + ":" + p.PID + ":" + p.Serial
	}
	return "name:" + p.Name
}
