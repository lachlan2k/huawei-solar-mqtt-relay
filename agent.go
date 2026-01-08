package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/lachlan2k/huawei-solar-mqtt-relay/internal/modbus"
	"github.com/lachlan2k/huawei-solar-mqtt-relay/internal/solar"
)

func runAgent(cfg *LoadedConfig) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mc, err := setupMqtt(cfg)
	if err != nil {
		slog.Error("mqtt setup", "err", err)
		os.Exit(1)
	}
	defer mc.Disconnect(2000)

	var inverter *solar.Client

	connectToInverter := func() error {
		if inverter != nil {
			inverter.Close()
			inverter = nil
		}

		var err error

		dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		inverter, err = setupInverter(dialCtx, cfg)
		if err != nil {
			slog.Error("failed to connect to inverter", "err", err)
			return err
		}

		err = inverter.BroadcastHello(cfg.broadcastDstIP, cfg.broadcastSelfIP)
		if err != nil {
			slog.Warn("problem when trying to broadcast hello message, proceeding anyway (normal when across VLANs/subnets)", "err", err)
		}

		go inverter.Run(ctx)
		return nil
	}

	err = connectToInverter()
	if err != nil {
		slog.Error("failed to connect to inverter", "err", err)
		os.Exit(1)
	}

	err = inverter.Login(ctx, cfg.Modbus.Username, cfg.Modbus.Password)
	if err != nil {
		slog.Warn("problem when trying to log in to inverter, proceeding anyway", "err", err)
	} else {
		slog.Info("successfully logged in")
	}

	handleQueryError := func(err error) {
		slog.Warn("query error", "err", err)
		slog.Info("attempting to login again (likely timed out)")

		err = inverter.Login(ctx, cfg.Modbus.Username, cfg.Modbus.Password)
		if err == nil {
			slog.Info("successfully logged in again")
			return
		}

		slog.Warn("failed to complete login again, restarting connection to inverter", "err", err)

		err = connectToInverter()
		backoff := time.Second

		attempts := 0
		for err != nil {
			slog.Warn("failed to connect to inverter", "err", err, "attempts", attempts, "retrying_in", backoff.Seconds())
			time.Sleep(backoff)
			err = connectToInverter()

			if backoff < (5 * time.Minute) {
				attempts++
				if attempts >= 10 {
					backoff *= 2
					attempts = 0
				}
			}
		}
	}

	// Inverter->MQTT message channel
	dataCh := make(chan *solar.Data, 10)

	// Query goroutine
	go func() {
		ticker := time.NewTicker(cfg.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return

			case <-ticker.C:
				if cfg.LogQuery {
					slog.Info("querying...")
				}

				d, err := inverter.Query(ctx)

				if err != nil {
					handleQueryError(err)
					continue
				}

				if cfg.LogQuery {
					slog.Info("query data", "data", d.Pretty())
				}

				select {
				case dataCh <- d:
				default:
					slog.Warn("data channel full, dropping sample (mqtt client sad?)")
				}
			}
		}
	}()

	// Publisher goroutine
	go func() {
		for {
			select {
			case <-ctx.Done():
				return

			case d := <-dataCh:
				if d == nil {
					continue
				}

				payload, err := json.Marshal(d)
				if err != nil {
					slog.Warn("marshal error when sending mqtt json", "err", err)
					continue
				}

				token := mc.Publish(cfg.MQTT.Topic, cfg.MQTT.QoS, cfg.MQTT.Retain, payload)
				if !token.WaitTimeout(5*time.Second) || token.Error() != nil {
					slog.Warn("mqtt publish error", "err", token.Error())
				}
			}
		}
	}()

	// Block until signal
	<-ctx.Done()
	slog.Info("exiting")
}

func setupMqtt(cfg *LoadedConfig) (mqtt.Client, error) {
	mopts := mqtt.NewClientOptions().AddBroker(cfg.MQTT.Broker).SetClientID(cfg.MQTT.ClientID)
	if cfg.MQTT.Username != "" {
		mopts.SetUsername(cfg.MQTT.Username)
		mopts.SetPassword(cfg.MQTT.Password)
	}
	mopts.SetAutoReconnect(true).SetConnectRetry(true).SetConnectTimeout(5 * time.Second)

	mc := mqtt.NewClient(mopts)
	token := mc.Connect()
	if !token.WaitTimeout(10*time.Second) || token.Error() != nil {
		return nil, token.Error()
	}
	return mc, nil
}

func setupInverter(ctx context.Context, cfg *LoadedConfig) (*solar.Client, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", cfg.Modbus.IP, cfg.Modbus.Port))
	if err != nil {
		return nil, fmt.Errorf("failed to dial modbus tcp: %v", err)
	}

	return solar.NewClient(modbus.NewModbusConn(conn, cfg.Modbus.SlaveID)), nil
}
