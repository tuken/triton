package serial

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"time"

	myctx "github.com/tuken/triton/context"
	"go.bug.st/serial"
)

const (
	DefaultTimeout = 4 * time.Second
)

type Com struct {
	portName  string
	port      serial.Port
	ctx       context.Context
	queryChan chan query
	done      chan struct{}
}

func (c *Com) loop() {

	defer close(c.done)

	for req := range c.queryChan {

		if err := writeAll(c.port, req.packet.Marshal()); err != nil {
			req.replyChan <- reply{err: err}
			continue
		}

		resp := make([]byte, req.reply.packet.FixedSize())

		if _, err := io.ReadFull(&portReader{p: c.port}, resp); err != nil {
			req.replyChan <- reply{err: err}
			continue
		}

		if err := req.reply.packet.Unmarshal(resp); err != nil {
			req.replyChan <- reply{err: err}
			continue
		}

		req.replyChan <- reply{packet: req.reply.packet}
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
	c.queryChan = make(chan query)
	c.done = make(chan struct{})

	go c.loop()

	return nil
}

func (c *Com) Disconnect() error {

	log := myctx.MustLogger(c.ctx)

	close(c.queryChan)
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
	packet    Marshaler
	reply     reply
	replyChan chan reply
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
