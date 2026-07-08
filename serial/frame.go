package serial

import (
	"errors"
	"fmt"
	"io"
)

// 受信フレームの Type（ヘッダ2バイト目）の値。
const (
	TypeUplinkNotify     byte = 0x00 // アップリンク通知（可変長）
	TypeDownlinkResponse byte = 0x01 // ダウンリンク応答（固定長）
	TypeInfoResponse     byte = 0x02 // Info レスポンス（可変長）
	TypeDFUResponse      byte = 0x03 // DFU レスポンス（固定長）
	TypeErrorNotify      byte = 0xFF // エラー通知（固定長）
)

// Frame 受信1フレームの共通インターフェース。
// フレームは先頭2バイト（[0]=ProtocolVersion, [1]=Type）が共通ヘッダで、
// 種別ごとに固定長部＋（必要なら）可変長部を持つ。可変長サイズは固定部の
// バイト列だけで決まるため、Unmarshal 前でも VariableSize を呼べる。
type Frame interface {

	// Unmarshal フレーム全体（固定部＋可変部）をパースする。
	Unmarshal(buf []byte) error

	// FixedSize 先に読み込むべき固定長部のサイズ（ヘッダ2バイトを含む）。
	FixedSize() int

	// VariableSize 固定部のバイト列 fixed から可変長部のサイズを返す。
	VariableSize(fixed []byte) int
}

// ErrReadTimeout SetReadTimeout で設定した時間内に1バイトも受信できなかった
// （フレーム境界での無通信）ことを表す。go.bug.st/serial はタイムアウト時に
// (0, nil) を返すため、それをこの sentinel エラーに変換して呼び出し側へ伝える。
// 外部からは errors.Is(err, router.ErrReadTimeout) で判定できる。
var ErrReadTimeout = errors.New("router: read timeout")

// newFrameByType ヘッダ2バイト目（Type）から、対応する空の Frame を生成する。
// ここがプロトコルの「種別 → どの構造体で受けるか」のディスパッチ表になる。
func newFrameByType(typ byte) (Frame, error) {

	switch typ {

	case TypeUplinkNotify:
		return &UplinkNotify{}, nil

	case TypeDownlinkResponse:
		return &DownlinkResponse{}, nil

	case TypeInfoResponse:
		return &InfoResponse{}, nil

	case TypeDFUResponse:
		return &DFUResponse{}, nil

	case TypeErrorNotify:
		return &ErrorNotify{}, nil

	default:
		return nil, fmt.Errorf("unknown frame type: 0x%02X", typ)
	}
}

// readFull r から len(buf) バイトを読み込む。io.ReadFull と異なり、
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

// readFrame r から1フレームを読み出し、種別(Type)と Unmarshal 済みの Frame を返す。
//
//  1. ヘッダ2バイトを読む（フレーム境界なので無通信時は ErrReadTimeout）
//  2. Type から受け取る構造体を決める
//  3. 固定長部まで残りを読む
//  4. 固定部から可変長サイズを求め、あればその分を追加で読む
//  5. 全体を1回だけ Unmarshal する
func readFrame(r io.Reader) (byte, Frame, error) {

	// 1) ヘッダ2バイト
	header := make([]byte, 2)
	if err := readFull(r, header, true); err != nil {
		return 0, nil, err
	}

	typ := header[1]

	// 2) 種別決定
	f, err := newFrameByType(typ)
	if err != nil {
		return typ, nil, err
	}

	// 3) 固定長部
	fixedSize := f.FixedSize()
	if fixedSize < 2 {
		return typ, nil, fmt.Errorf("invalid fixed size %d for type 0x%02X", fixedSize, typ)
	}

	buf := make([]byte, fixedSize)
	copy(buf, header)

	if err := readFull(r, buf[2:], false); err != nil {
		return typ, nil, err
	}

	// 4) 可変長部
	if varSize := f.VariableSize(buf); varSize > 0 {

		full := make([]byte, fixedSize+varSize)
		copy(full, buf)

		if err := readFull(r, full[fixedSize:], false); err != nil {
			return typ, nil, err
		}

		buf = full
	}

	// 5) Unmarshal
	if err := f.Unmarshal(buf); err != nil {
		return typ, nil, err
	}

	return typ, f, nil
}
