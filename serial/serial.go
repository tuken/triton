package serial

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"time"

	myctx "github.com/tuken/triton/context"
	"go.bug.st/serial"
)

const (
	DefaultTimeout = 4 * time.Second
)

type Com struct {
	portName string
	port     serial.Port
	ctx      context.Context
	queryCh  chan query
	readMu   sync.Mutex
	done     chan struct{}
}

// Event は1回の読み取り結果を表す。
// Packet が非nilなら受信フレーム、Err が非nilなら読み取りエラー。
// 無通信タイムアウトは Err == ErrReadTimeout（errors.Is で判定可）で通知される。
type Event struct {
	Packet Unmarshaler
	Err    error
}

// Read は1フレームを非同期に読み取り、その結果を受け取るチャネルを返す。
// 常時読み込みを行う代わりに、呼び出し側が読みたいタイミングで Read を呼ぶ。
//
// 返り値のチャネルはバッファ付き（cap 1）なので、呼び出し側が ctx などで
// 受信を諦めても内部 goroutine はブロックせずに終了する。ポートへの同時
// Read を防ぐため readMu で直列化しており、前回の読み取りが完了するまで
// 次の読み取りは開始されない。
//
//	select {
//	case ev := <-com.Read():
//	    // ev.Err / ev.Packet を処理
//	case <-ctx.Done():
//	    // 今回の読み取りを諦める（次の Read で再開）
//	}
func (c *Com) Read() <-chan Event {

	ch := make(chan Event, 1)

	go func() {
		c.readMu.Lock()
		defer c.readMu.Unlock()

		pkt, err := readFrame(&portReader{p: c.port})
		ch <- Event{Packet: pkt, Err: err}
	}()

	return ch
}

func (c *Com) loop() {

	defer close(c.done)

	for req := range c.queryCh {

		if err := writeAll(c.port, req.packet.Marshal()); err != nil {
			req.replyCh <- reply{err: err}
			continue
		}

		resp := make([]byte, req.reply.packet.FixedSize())

		if _, err := io.ReadFull(&portReader{p: c.port}, resp); err != nil {
			req.replyCh <- reply{err: err}
			continue
		}

		if err := req.reply.packet.Unmarshal(resp); err != nil {
			req.replyCh <- reply{err: err}
			continue
		}

		req.replyCh <- reply{packet: req.reply.packet}
	}
}

func (c *Com) writerLoop() {

	for req := range c.queryCh {

		// c.inflightMu.Lock()
		// c.inflight = &req
		// c.inflightMu.Unlock()

		if err := writeAll(c.port, req.packet.Marshal()); err != nil {
			req.replyCh <- reply{err: err}
			// c.inflightMu.Lock()
			// c.inflight = nil
			// c.inflightMu.Unlock()
			continue
		}
		// 応答待ちは Do 側が replyCh で待つ
	}
}

func (c *Com) Connect(ctx context.Context, portName string, mode *serial.Mode) error {

	log := myctx.MustLogger(ctx)

	// COMポートをオープン
	port, err := serial.Open(portName, mode)
	if err != nil {
		log.Fatalw("open error", "portName", portName, "error", err)
		return err
	}

	// 接続したことがあるルーターはKeepAlive要求を通知してくる、初回接続のものは何も通知してこない。このため、接続時はタイムアウトを30秒にして、KeepAlive要求が来るのを待つ。
	if err := c.port.SetReadTimeout(30 * time.Second); err != nil {
		log.Errorw("set timeout error", "portName", c.portName, "error", err)
		return err
	}

	discard := make([]byte, 7)
	n, err := c.port.Read(discard)
	if err != nil {
		log.Errorw("read error", "portName", c.portName, "error", err)
		return err
	}

	if n != 0 {
		log.Infow("Keep Alive 要求受信")
	} else {
		log.Infow("Keep Alive 要求なし")
	}

	// 読み取り待ちで永久ブロックしないようにデフォルト（4秒）のタイムアウト値にしておく
	if err := port.SetReadTimeout(DefaultTimeout); err != nil {
		log.Fatalw("set timeout error", "portName", portName, "error", err)
		return err
	}

	c.portName = portName
	c.port = port
	c.ctx = ctx
	c.queryCh = make(chan query)
	c.done = make(chan struct{})

	go c.loop()

	return nil
}

func (c *Com) Disconnect() error {

	log := myctx.MustLogger(c.ctx)

	close(c.queryCh)
	<-c.done

	if c.port != nil {

		if err := c.port.Close(); err != nil {
			log.Errorw("close error", "portName", c.portName, "error", err)
			return err
		}

		c.port = nil
	}

	return nil
}

func (c *Com) SendKeepAlive() error {

	log := myctx.MustLogger(c.ctx)

	unixTime := uint32(time.Now().Unix())
	localTime := unixTime + 9*3600

	cmd := make([]byte, 0, 11)
	cmd = append(cmd, 0x01, 0x01, 0xD0)
	cmd = binary.LittleEndian.AppendUint32(cmd, localTime)
	cmd = binary.LittleEndian.AppendUint32(cmd, unixTime)

	_, err := c.port.Write(cmd)
	if err != nil {
		log.Errorw("write error", "portName", c.portName, "error", err)
		return err
	}

	log.Infow("Keep Alive 送信")

	return nil
}

func writeAll(p serial.Port, b []byte) error {

	for len(b) > 0 {

		n, err := p.Write(b)
		if err != nil {
			return err
		}

		if n == 0 {
			return errors.New("short write")
		}

		b = b[n:]
	}

	return nil
}

type query struct {
	packet  Marshaler
	reply   reply
	replyCh chan reply
}

type reply struct {
	packet Unmarshaler
	err    error
}

type portReader struct {
	p serial.Port
}

func (r *portReader) Read(b []byte) (int, error) {

	return r.p.Read(b)
}
