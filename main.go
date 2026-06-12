package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/influxdata/influxdb-client-go/v2/api"
	myctx "github.com/tuken/triton/context"
	"github.com/tuken/triton/logger"
	myser "github.com/tuken/triton/serial"
	"go.bug.st/serial"
)

func main() {

	log := logger.NewLogger()
	ctx := context.WithValue(context.Background(), myctx.LoggerKey, log)

	com := &myser.Com{}

	log.Infow("アプリケーション起動")

	// COMポートをオープン
	if err := com.Connect(ctx, "/dev/ttyACM0", &serial.Mode{
		BaudRate: 115200,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	}); err != nil {
		log.Fatalw("USB接続エラー", "error", err)
		return
	}

	defer com.Disconnect()

	paired, err := com.WaitKeepAliveRequest()
	if err != nil {
		log.Fatalw("KeepAlive要求待機エラー", "error", err)
		return
	}

	if paired {
		log.Infow("初めての接続")
	}

	// InfluxDB 接続
	app.influx = influxdb2.NewClient(cfg.InfluxDBURL, cfg.InfluxDBToken)
	app.writeAPI = app.influx.WriteAPIBlocking(cfg.InfluxDBOrg, cfg.InfluxDBBucket)

	// MQTT 接続
	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tcp://%s:%d", cfg.MQTTBroker, cfg.MQTTPort))
	opts.SetAutoReconnect(true)
	opts.OnConnect = func(client mqtt.Client) {
		log.Infow("[MQTT] 接続成功")
		client.Subscribe("bravejig/cmd/interval", 0, app.onIntervalCommand)
		client.Subscribe("bravejig/cmd/send_now", 0, app.onSendNowCommand)
		log.Printf("[MQTT] 購読完了: bravejig/cmd/interval, bravejig/cmd/send_now")
	}
	opts.OnConnectionLost = func(client mqtt.Client, err error) {
		log.Printf("[MQTT] 切断: %v", err)
	}
	app.mqttClient = mqtt.NewClient(opts)
	if token := app.mqttClient.Connect(); token.Wait() && token.Error() != nil {
		log.Fatalf("[MQTT] 接続失敗: %v", token.Error())
	}
	log.Printf("[MQTT] クライアント起動")

	// シグナルハンドリング
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		log.Printf("終了")
		app.mqttClient.Disconnect(250)
		app.influx.Close()
		os.Exit(0)
	}()

	app.run()
}

// ── SensorID定義 ──────────────────────────────────
const (
	SensorTH       = 0x0123 // 温湿度
	SensorLux      = 0x0121 // 照度
	SensorPressure = 0x0124 // 気圧
	HeaderSize     = 21     // センサーデータ開始オフセット
)

var sensorKeyToID = map[string]uint16{
	"th":       SensorTH,
	"pressure": SensorPressure,
	"lux":      SensorLux,
}

// 有効なUNIXタイムスタンプの範囲（2024〜2035年）
const (
	tsMin = 1704067200 // 2024-01-01
	tsMax = 2051222400 // 2035-01-01
)

// App はアプリケーション全体の状態を保持する。
type App struct {
	cfg        Config
	mqttClient mqtt.Client
	writeAPI   api.WriteAPIBlocking
	influx     influxdb2.Client

	mu         sync.Mutex
	currentSer serial.Port // ダウンリンク送信用の現在のシリアルポート
}

// ── ダウンリンク/コマンド ─────────────────────────

// sendSetInterval はダウンリンク CMD=0x05 (SET_REGISTER) でUplink間隔を変更する。
// パケット形式: [0x01, CMD=0x05, SensorID(2byte LE), Interval(4byte LE)]
func sendSetInterval(ser serial.Port, sensorID uint16, intervalSeconds uint32) {
	cmd := make([]byte, 0, 8)
	cmd = append(cmd, 0x01, 0x05)
	cmd = binary.LittleEndian.AppendUint16(cmd, sensorID)
	cmd = binary.LittleEndian.AppendUint32(cmd, intervalSeconds)
	ser.Write(cmd)
	log.Printf("[ダウンリンク送信] SensorID=0x%04x, interval=%ds (%d分), 16進: [%s]",
		sensorID, intervalSeconds, intervalSeconds/60, hexBytes(cmd))
}

// onIntervalCommand はMQTT経由でUplink間隔変更コマンドを受信する。
func (a *App) onIntervalCommand(client mqtt.Client, message mqtt.Message) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("インターバル設定エラー: %v", r)
		}
	}()
	log.Printf("[MQTT] メッセージ受信: %s", message.Payload())
	var data struct {
		Sensor   string `json:"sensor"`
		Interval int    `json:"interval"`
	}
	if err := json.Unmarshal(message.Payload(), &data); err != nil {
		log.Printf("インターバル設定エラー: %v", err)
		return
	}
	log.Printf("[MQTT] パース完了: sensor=%s, interval=%d", data.Sensor, data.Interval)
	sensorID, ok := sensorKeyToID[data.Sensor]
	if !ok {
		log.Printf("不明なセンサーキー: %s", data.Sensor)
		return
	}
	a.mu.Lock()
	ser := a.currentSer
	a.mu.Unlock()
	if ser != nil {
		log.Printf("[MQTT] ダウンリンク送信を実行")
		sendSetInterval(ser, sensorID, uint32(data.Interval))
	} else {
		log.Printf("シリアルポート未接続のためダウンリンク送信スキップ")
	}
}

// onSendNowCommand はMQTT経由で即時Uplink要求コマンドを受信する。
func (a *App) onSendNowCommand(client mqtt.Client, message mqtt.Message) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("即時Uplink要求エラー: %v", r)
		}
	}()
	log.Printf("[MQTT] 即時Uplink要求受信: %s", message.Payload())
	var data struct {
		Sensor string `json:"sensor"`
	}
	if err := json.Unmarshal(message.Payload(), &data); err != nil {
		log.Printf("即時Uplink要求エラー: %v", err)
		return
	}
	log.Printf("[MQTT] パース完了: sensor=%s", data.Sensor)
	sensorID, ok := sensorKeyToID[data.Sensor]
	if !ok {
		log.Printf("不明なセンサーキー: %s", data.Sensor)
		return
	}
	a.mu.Lock()
	ser := a.currentSer
	a.mu.Unlock()
	if ser != nil {
		log.Printf("[MQTT] 即時Uplink送信を実行")
		cmd := make([]byte, 0, 4)
		cmd = append(cmd, 0x01, 0x00)
		cmd = binary.LittleEndian.AppendUint16(cmd, sensorID)
		ser.Write(cmd)
		log.Printf("[即時Uplink要求] SensorID=0x%04x, 16進: [%s]", sensorID, hexBytes(cmd))
	} else {
		log.Printf("シリアルポート未接続のため即時Uplink送信スキップ")
	}
}

func sendKeepalive(ser serial.Port) {
	unixTime := uint32(time.Now().Unix())
	localTime := unixTime + 9*3600
	cmd := make([]byte, 0, 11)
	cmd = append(cmd, 0x01, 0x01, 0xD0)
	cmd = binary.LittleEndian.AppendUint32(cmd, localTime)
	cmd = binary.LittleEndian.AppendUint32(cmd, unixTime)
	ser.Write(cmd)
	log.Printf("Keep Alive送信完了")
}

// ── パケット解析 ─────────────────────────────────

func (a *App) parsePacket(pkt []byte) {
	if len(pkt) < HeaderSize+1 {
		return
	}
	if pkt[1] == 0xFF {
		var code byte
		if len(pkt) > 6 {
			code = pkt[6]
		}
		log.Printf("エラー通知: 0x%02x", code)
		return
	}
	if pkt[1] != 0x00 {
		return
	}
	sensorID := binary.LittleEndian.Uint16(pkt[16:18])
	sd := pkt[HeaderSize:]
	if len(sd) < 12 {
		return
	}
	battery := sd[0]
	value := math.Float32frombits(binary.LittleEndian.Uint32(sd[8:12]))

	switch sensorID {
	case SensorTH:
		if len(sd) < 16 {
			return
		}
		temp := math.Float32frombits(binary.LittleEndian.Uint32(sd[8:12]))
		hum := math.Float32frombits(binary.LittleEndian.Uint32(sd[12:16]))
		log.Printf("[温湿度] %.1f℃  %.1f%%  bat:%d%%", temp, hum, battery)
		payload, _ := json.Marshal(map[string]any{
			"temperature": round2(float64(temp)),
			"humidity":    round2(float64(hum)),
			"battery":     battery,
		})
		a.mqttClient.Publish("bravejig/sensors/th", 0, false, payload)
		p := influxdb2.NewPoint("environment",
			map[string]string{"sensor": "bravejig_th", "location": "demo", "house_id": "1"},
			map[string]any{
				"temperature": round2(float64(temp)),
				"humidity":    round2(float64(hum)),
				"battery":     int(battery),
			}, time.Now())
		if err := a.writeAPI.WritePoint(context.Background(), p); err != nil {
			log.Printf("InfluxDB書き込みエラー [温湿度]: %v", err)
		} else {
			log.Printf("InfluxDB書き込み完了 [温湿度]")
		}

	case SensorPressure:
		pressure := value
		log.Printf("[気圧] %.1fhPa  bat:%d%%", pressure, battery)
		payload, _ := json.Marshal(map[string]any{
			"pressure": round2(float64(pressure)),
			"battery":  battery,
		})
		a.mqttClient.Publish("bravejig/sensors/pressure", 0, false, payload)
		p := influxdb2.NewPoint("environment",
			map[string]string{"sensor": "bravejig_pressure", "location": "demo", "house_id": "1"},
			map[string]any{
				"pressure": round2(float64(pressure)),
				"battery":  int(battery),
			}, time.Now())
		if err := a.writeAPI.WritePoint(context.Background(), p); err != nil {
			log.Printf("InfluxDB書き込みエラー [気圧]: %v", err)
		} else {
			log.Printf("InfluxDB書き込み完了 [気圧]")
		}

	case SensorLux:
		lux := value
		log.Printf("[照度] %.1fLux  bat:%d%%", lux, battery)
		payload, _ := json.Marshal(map[string]any{
			"lux":     round2(float64(lux)),
			"battery": battery,
		})
		a.mqttClient.Publish("bravejig/sensors/lux", 0, false, payload)
		p := influxdb2.NewPoint("environment",
			map[string]string{"sensor": "bravejig_lux", "location": "demo", "house_id": "1"},
			map[string]any{
				"lux":     round2(float64(lux)),
				"battery": int(battery),
			}, time.Now())
		if err := a.writeAPI.WritePoint(context.Background(), p); err != nil {
			log.Printf("InfluxDB書き込みエラー [照度]: %v", err)
		} else {
			log.Printf("InfluxDB書き込み完了 [照度]")
		}

	default:
		log.Printf("未知SensorID: 0x%04x", sensorID)
	}
}

// isValidPacketStart は有効なパケット先頭バイト列かチェックする。
func isValidPacketStart(buf []byte) bool {
	if len(buf) < 8 {
		return false
	}
	if buf[0] != 0x01 {
		return false
	}
	if buf[1] != 0x00 && buf[1] != 0xFF {
		return false
	}
	// Data Length の上位バイト(buf[3])は必ず0x00のはず
	if buf[3] != 0x00 {
		return false
	}
	// タイプ0x00（アップリンク）はUNIXタイムスタンプで追加検証
	if buf[1] == 0x00 {
		ts := binary.LittleEndian.Uint32(buf[4:8])
		if ts < tsMin || ts > tsMax {
			return false
		}
	}
	return true
}

// splitPackets はパケット総長 = 21 + byte[2] で可変長分割する。
func splitPackets(buf []byte) (packets [][]byte, rest []byte) {
	for len(buf) >= 22 {
		if !isValidPacketStart(buf) {
			// 次の有効なパケット先頭を探す
			idx := 1
			for idx < len(buf) {
				if buf[idx] == 0x01 && isValidPacketStart(buf[idx:]) {
					break
				}
				idx++
			}
			if idx >= len(buf) {
				skipped := hexBytesPlain(buf)
				if len(skipped) > 60 {
					skipped = skipped[:60]
				}
				log.Printf("同期ずれ検出、バッファクリア(%dバイト): [%s...]", len(buf), skipped)
				buf = nil
				break
			}
			log.Printf("同期ずれ検出、%dバイトスキップ: [%s]", idx, hexBytesPlain(buf[:idx]))
			buf = buf[idx:]
			continue
		}
		pktType := buf[1]
		var total int
		// Type=0xFF（エラー通知）は固定7バイト
		if pktType == 0xFF {
			total = 7
		} else {
			dataLen := int(buf[2])
			total = 21 + dataLen
		}
		if len(buf) < total {
			break // 次の受信を待つ
		}
		pkt := make([]byte, total)
		copy(pkt, buf[:total])
		packets = append(packets, pkt)
		buf = buf[total:]
	}
	return packets, buf
}

// ── シリアル接続 ─────────────────────────────────

func (a *App) connectSerial() serial.Port {
	mode := &serial.Mode{BaudRate: a.cfg.SerialBaud}
	for {
		ser, err := serial.Open(a.cfg.SerialPort, mode)
		if err != nil {
			log.Printf("接続失敗: %v → 10秒後リトライ", err)
			time.Sleep(10 * time.Second)
			continue
		}
		ser.SetReadTimeout(10 * time.Second)
		log.Printf("接続成功: %s", a.cfg.SerialPort)
		time.Sleep(500 * time.Millisecond)
		sendKeepalive(ser)
		log.Printf("受信待機中...")
		return ser
	}
}

func (a *App) run() {
	ser := a.connectSerial()
	a.mu.Lock()
	a.currentSer = ser
	a.mu.Unlock()

	buf := make([]byte, 0, 512)
	lastKA := time.Now()
	chunk := make([]byte, 256)
	for {
		if time.Since(lastKA) > 30*time.Second {
			sendKeepalive(ser)
			lastKA = time.Now()
		}
		n, err := ser.Read(chunk)
		if err != nil {
			log.Printf("シリアルエラー → 再接続: %v", err)
			ser.Close()
			time.Sleep(3 * time.Second)
			ser = a.connectSerial()
			a.mu.Lock()
			a.currentSer = ser
			a.mu.Unlock()
			buf = buf[:0]
			lastKA = time.Now()
			continue
		}
		if n == 0 {
			continue
		}
		buf = append(buf, chunk[:n]...)
		var packets [][]byte
		packets, buf = splitPackets(buf)
		for _, pkt := range packets {
			a.parsePacket(pkt)
		}
	}
}

// ── ヘルパー ─────────────────────────────────────

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func hexBytes(b []byte) string {
	s := ""
	for i, v := range b {
		if i > 0 {
			s += " "
		}
		s += fmt.Sprintf("0x%02x", v)
	}
	return s
}

func hexBytesPlain(b []byte) string {
	s := ""
	for i, v := range b {
		if i > 0 {
			s += " "
		}
		s += fmt.Sprintf("%02x", v)
	}
	return s
}

// ── main ─────────────────────────────────────────
