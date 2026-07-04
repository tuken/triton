package serial

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	myctx "github.com/tuken/triton/context"
	"go.bug.st/serial"
	"go.uber.org/zap"
)

const (
	// DefaultTimeout は Read の1回あたりの待ち時間。境界でこの時間を超えると
	// ErrReadTimeout として扱い、Run はループを一巡して ctx を確認する。
	DefaultTimeout = 4 * time.Second
)

// Handler は受信フレームを処理する関数。登録した型のフレームが届くと呼ばれる。
// dispatch は Run の読み取りループ内で同期的に呼ぶため、重い処理は自前の
// goroutine に逃がすこと（さもないと後続フレームの読み取りが滞る）。
type Handler func(*Com, Frame)

// Com は go.bug.st/serial を使った USB シリアル通信のクライアント。
type Com struct {
	portName string
	port     serial.Port

	mu       sync.RWMutex
	handlers map[byte]Handler

	writeMu sync.Mutex

	ctx context.Context
}

// NewCom は空の Com を生成する。
func NewCom(ctx context.Context) *Com {
	return &Com{
		handlers: make(map[byte]Handler),
		ctx:      ctx,
	}
}

// Handle は種別 typ のフレームを受信したときに呼ぶハンドラを登録する。
// 同じ型に再登録すると上書きする。nil を渡すと登録解除。
func (c *Com) Handle(typ byte, h Handler) {

	c.mu.Lock()
	defer c.mu.Unlock()

	if h == nil {
		delete(c.handlers, typ)
		return
	}

	c.handlers[typ] = h
}

func (c *Com) Context() context.Context {

	return c.ctx
}

// Connect は COM ポートを開く。
func (c *Com) Connect(portName string, mode *serial.Mode) error {

	port, err := serial.Open(portName, mode)
	if err != nil {
		return fmt.Errorf("open %s: %w", portName, err)
	}

	// 境界での無通信を ErrReadTimeout として検知できるようタイムアウトを設定する。
	if err := port.SetReadTimeout(DefaultTimeout); err != nil {
		_ = port.Close()
		return fmt.Errorf("set read timeout: %w", err)
	}

	c.portName = portName
	c.port = port

	return nil
}

// Disconnect はポートを閉じる。Run 実行中に呼ぶと、ブロック中の Read が解除され
// Run は nil を返して終了する。
func (c *Com) Disconnect() error {

	if c.port == nil {
		return nil
	}

	err := c.port.Close()
	c.port = nil

	if err != nil {
		return fmt.Errorf("close %s: %w", c.portName, err)
	}

	return nil
}

// Run は常時 Read し続け、1フレームごとに登録ハンドラへディスパッチする。
// 次のいずれかで終了する:
//
//   - ctx がキャンセルされた（ctx.Err() を返す）
//   - Disconnect でポートが閉じられた（nil を返す）
//   - 回復不能な読み取り/パースエラー（そのエラーを返す）
//
// 無通信タイムアウト（ErrReadTimeout）は正常系として読み飛ばして継続する。
func (c *Com) Run() error {

	if c.port == nil {
		return errors.New("router: not connected")
	}

	log := myctx.MustLogger(c.ctx)
	r := &portReader{p: c.port}

	for {

		// ctx キャンセルを毎ループ確認する（Read は最大 DefaultTimeout でリターンする）
		select {
		case <-c.ctx.Done():
			return c.ctx.Err()
		default:
		}

		typ, frame, err := readFrame(r)
		if err != nil {

			// 無通信タイムアウト：フレーム境界で待ち時間経過。継続。
			if errors.Is(err, ErrReadTimeout) {
				continue
			}

			// Disconnect/Close によるポートクローズは正常終了とみなす。
			var portErr *serial.PortError
			if errors.As(err, &portErr) && portErr.Code() == serial.PortClosed {
				log.Infow("reader stopped (port closed)", "portName", c.portName)
				return nil
			}

			// 未知の型やパース失敗などは、その場では致命的として終了する。
			// （ストリームの同期がずれている可能性があるため）
			log.Errorw("read frame error", "portName", c.portName, "error", err)
			return err
		}

		c.dispatch(log, typ, frame)
	}
}

// dispatch は typ に対応するハンドラを呼ぶ。未登録なら何もしない。
func (c *Com) dispatch(log *zap.SugaredLogger, typ byte, frame Frame) {

	c.mu.RLock()
	h := c.handlers[typ]
	c.mu.RUnlock()

	if h == nil {
		log.Debugw("no handler for frame", "type", fmt.Sprintf("0x%02X", typ))
		return
	}

	h(c, frame)
}

// Write は1つのリクエストフレームを送信する。送信は writeMu で直列化する。
func (c *Com) Write(m Marshaler) error {

	if c.port == nil {
		return errors.New("router: not connected")
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	return writeAll(c.port, m.Marshal())
}

func writeAll(p serial.Port, b []byte) error {

	for len(b) > 0 {

		n, err := p.Write(b)
		if err != nil {
			return err
		}

		if n == 0 {
			return errors.New("router: short write")
		}

		b = b[n:]
	}

	return nil
}

// portReader は serial.Port を io.Reader に適合させる。
type portReader struct {
	p serial.Port
}

func (r *portReader) Read(b []byte) (int, error) {
	return r.p.Read(b)
}
