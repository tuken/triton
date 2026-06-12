package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"go.bug.st/serial"
)

type txReq struct {
	cmd     []byte
	respLen int
	replyCh chan txResp
}

type txResp struct {
	data []byte
	err  error
}

type Client struct {
	port  serial.Port
	reqCh chan txReq
	done  chan struct{}
}

func NewClient(port serial.Port) *Client {
	c := &Client{
		port:  port,
		reqCh: make(chan txReq),
		done:  make(chan struct{}),
	}
	go c.ioLoop()
	return c
}

func (c *Client) Close() error {
	close(c.reqCh)
	<-c.done
	return c.port.Close()
}

// Do sends one fixed-length command and waits for one fixed-length response.
func (c *Client) Do(ctx context.Context, cmd []byte, respLen int) ([]byte, error) {
	replyCh := make(chan txResp, 1)
	req := txReq{
		cmd:     append([]byte(nil), cmd...), // defensive copy
		respLen: respLen,
		replyCh: replyCh,
	}

	select {
	case c.reqCh <- req:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	select {
	case r := <-replyCh:
		return r.data, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Client) ioLoop() {
	defer close(c.done)

	for req := range c.reqCh {
		// 1) write all
		if err := writeAll(c.port, req.cmd); err != nil {
			req.replyCh <- txResp{err: err}
			continue
		}

		// 2) read exact response length
		resp := make([]byte, req.respLen)
		if _, err := io.ReadFull(&portReader{p: c.port}, resp); err != nil {
			req.replyCh <- txResp{err: err}
			continue
		}

		req.replyCh <- txResp{data: resp}
	}
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

// serial.Port has Read method, but it is not io.Reader interface type directly.
// Wrap it for io.ReadFull.
type portReader struct{ p serial.Port }

func (r *portReader) Read(b []byte) (int, error) {
	return r.p.Read(b)
}

func main() {
	mode := &serial.Mode{
		BaudRate: 115200,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	}
	port, err := serial.Open("/dev/ttyACM0", mode)
	if err != nil {
		panic(err)
	}
	_ = port.SetReadTimeout(2 * time.Second)

	client := NewClient(port)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := []byte{0x01, 0x10, 0x00, 0x00} // fixed-length command example
	resp, err := client.Do(ctx, cmd, 8)   // fixed-length response example
	fmt.Printf("resp=% X err=%v\n", resp, err)
}

// -----------------------------------------
type txReq struct {
	cmd       []byte
	respMatch func([]byte) bool // どの受信が自分の応答か
	replyCh   chan txResp
}

type Client struct {
	port    serial.Port
	reqCh   chan txReq
	eventCh chan []byte
	done    chan struct{}

	// 同時に1コマンドだけ流すなら mutex だけで十分
	inflightMu sync.Mutex
	inflight   *txReq
}

func (c *Client) Start() {
	go c.writerLoop()
	go c.readerLoop()
}

func (c *Client) writerLoop() {
	for req := range c.reqCh {
		c.inflightMu.Lock()
		c.inflight = &req
		c.inflightMu.Unlock()

		if err := writeAll(c.port, req.cmd); err != nil {
			req.replyCh <- txResp{err: err}
			c.inflightMu.Lock()
			c.inflight = nil
			c.inflightMu.Unlock()
			continue
		}
		// 応答待ちは Do 側が replyCh で待つ
	}
}

func (c *Client) readerLoop() {
	defer close(c.done)

	for {
		frame := make([]byte, 8) // 受信固定長に合わせる
		_, err := io.ReadFull(&portReader{p: c.port}, frame)
		if err != nil {
			return
		}

		c.inflightMu.Lock()
		req := c.inflight
		if req != nil && req.respMatch(frame) {
			req.replyCh <- txResp{data: frame}
			c.inflight = nil
			c.inflightMu.Unlock()
			continue
		}
		c.inflightMu.Unlock()

		// 自発通知
		select {
		case c.eventCh <- frame:
		default:
			// 必要ならドロップ/ログ
		}
	}
}
