package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	myctx "github.com/tuken/triton/context"
	"github.com/tuken/triton/logger"
	myser "github.com/tuken/triton/serial"
	"github.com/tuken/triton/usb"
	"go.bug.st/serial"
)

func main() {

	log := logger.NewLogger()
	ctx := context.WithValue(context.Background(), myctx.LoggerKey, log)

	watch := usb.NewWatch(ctx)

	port := watch.Find("BraveJIG Router")
	if port != "" {
		fmt.Println("既に接続されている:", port)
	}

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
