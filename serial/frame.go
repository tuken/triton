package serial

import (
	"errors"
	"fmt"
	"io"
)

// ErrReadTimeout は SetReadTimeout で設定した時間内に1バイトも受信
// できなかった（フレーム境界での無通信）ことを表す。
// go.bug.st/serial はタイムアウト時に (0, nil) を返すため、それを
// この sentinel エラーに変換して呼び出し側へ伝える。外部からは
// errors.Is(ev.Err, serial.ErrReadTimeout) で判定できる。
var ErrReadTimeout = errors.New("serial: read timeout")

// readFull は r から len(buf) バイトを読み込む。io.ReadFull と異なり、
// go.bug.st/serial がタイムアウト時に返す (0, nil) を次のように扱う:
//
//   - boundary=true でまだ1バイトも読めていない場合: ErrReadTimeout を返す
//     （フレーム境界での無通信検知。呼び出し側が再送・監視等に使える）
//   - それ以外（フレーム途中のタイムアウト）: ストリームの同期ずれを防ぐため、
//     読み込みを継続して残りのバイトを待つ
func readFull(r io.Reader, buf []byte, boundary bool) error {

	n := 0

	for n < len(buf) {

		nn, err := r.Read(buf[n:])
		if err != nil {
			return err
		}

		if nn == 0 {

			// タイムアウト発生
			if boundary && n == 0 {
				return ErrReadTimeout
			}

			// フレーム途中なので継続して残りを待つ
			continue
		}

		n += nn
	}

	return nil
}

// 受信パケットの Type（ヘッダ2バイト目）の値。
const (
	TypeUplinkNotify     = 0x00 // アップリンク通知（可変長）
	TypeDownlinkResponse = 0x01 // ダウンリンクレスポンス（固定長）
	TypeInfoResponse     = 0x02 // Info レスポンス（可変長）
	TypeDFUResponse      = 0x03 // DFU レスポンス（固定長）
	TypeErrorNotify      = 0xFF // エラー通知（固定長）
)

// newPacketByType はヘッダ2バイト目（Type）から、対応する空の Unmarshaler を生成する。
// ここがプロトコルの「種別 → どの構造体で受けるか」のディスパッチ表になる。
func newPacketByType(typ byte) (Unmarshaler, error) {

	switch typ {

	case TypeUplinkNotify: // 0x00
		return &UplinkNotify{}, nil

	case TypeDownlinkResponse: // 0x01
		return &DownlinkResponse{}, nil

	case TypeInfoResponse: // 0x02
		return &InfoResponse{}, nil

	case TypeDFUResponse: // 0x03
		return &DFUResponse{}, nil

	case TypeErrorNotify: // 0xFF
		return &ErrorNotify{}, nil

	default:
		return nil, fmt.Errorf("unknown packet type: 0x%02X", typ)
	}
}

// readFrame は r から1フレームを読み出して、Unmarshal 済みのパケットを返す。
//
//  1. ヘッダ2バイトを読む（[0]=ProtocolVersion, [1]=Type）。ここはフレーム境界
//     なので、無通信タイムアウト時は ErrReadTimeout を返す。
//  2. Type から受け取る構造体を決める
//  3. その構造体の固定長分まで残りを読む
//  4. 固定部のバイト列から可変長サイズを求め、あればその分を追加で読む
//  5. 最後に1回だけ Unmarshal する
//
// r には io.Reader（例: *portReader）を渡す。
func readFrame(r io.Reader) (Unmarshaler, error) {

	// 1) まずヘッダ2バイトだけ読む（フレーム境界なのでタイムアウトを通知する）
	header := make([]byte, 2)
	if err := readFull(r, header, true); err != nil {
		return nil, err
	}

	// 2) 2バイト目（Type）で種別を決定
	pkt, err := newPacketByType(header[1])
	if err != nil {
		return nil, err
	}

	// 3) 固定長サイズまで残りを読み込む
	fixedSize := pkt.FixedSize()
	if fixedSize < 2 {
		return nil, fmt.Errorf("invalid fixed size %d for type 0x%02X", fixedSize, header[1])
	}

	buf := make([]byte, fixedSize)
	copy(buf, header)

	if err := readFull(r, buf[2:], false); err != nil {
		return nil, err
	}

	// 4) 固定部のバイト列から可変長サイズを求め、あれば追加で読み込む
	//    VariableSize は固定部バイト列だけで決まるので Unmarshal 前に呼べる
	if varSize := pkt.VariableSize(buf); varSize > 0 {

		full := make([]byte, fixedSize+varSize)
		copy(full, buf)

		if err := readFull(r, full[fixedSize:], false); err != nil {
			return nil, err
		}

		buf = full
	}

	// 5) 全体を1回だけ Unmarshal する
	if err := pkt.Unmarshal(buf); err != nil {
		return nil, err
	}

	return pkt, nil
}
