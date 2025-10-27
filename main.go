package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"gopkg.in/yaml.v3"

	"github.com/lachlan2k/huawei-solar-mqtt-relay/internal/modbus"
	"github.com/lachlan2k/huawei-solar-mqtt-relay/internal/solar"
)

type Config struct {
	Modbus struct {
		IP       string `yaml:"ip"`
		Port     uint16 `yaml:"port"`
		SlaveID  uint8  `yaml:"slave_id"`
		Username string `yaml:"username"`
		Password string `yaml:"password"`
	} `yaml:"modbus"`

	MQTT struct {
		Broker   string `yaml:"broker"`
		Topic    string `yaml:"topic"`
		ClientID string `yaml:"client_id"`
		Username string `yaml:"username"`
		Password string `yaml:"password"`
		QoS      byte   `yaml:"qos"`
		Retain   bool   `yaml:"retain"`
	} `yaml:"mqtt"`

	Broadcast struct {
		DestinationIP string `yaml:"destination_ip"`
		SelfIP        string `yaml:"self_ip"`
	} `yaml:"broadcast"`

	Interval string `yaml:"interval"`
	LogQuery bool   `yaml:"log_query"`
}

func loadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func main() {
	slog.SetLogLoggerLevel(slog.LevelDebug)

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	switch cmd {
	case "agent":
		runAgent(os.Args[2:])
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Printf("unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(2)
	}
}

func printUsage() {
	fmt.Println("Usage:")
	fmt.Println("  solar-agent agent -config config.yaml")
}

func runAgent(args []string) {
	fs := flag.NewFlagSet("agent", flag.ExitOnError)
	cfgPath := fs.String("config", "config.yaml", "Path to YAML config file")
	_ = fs.Parse(args)

	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}

	interval := 30 * time.Second
	if cfg.Interval != "" {
		if d, err := time.ParseDuration(cfg.Interval); err == nil {
			interval = d
		} else {
			slog.Warn("invalid interval", "interval", cfg.Interval, "err", err)
		}
	}

	// Inverter client config etc
	broadcastDstIP := net.ParseIP(cfg.Broadcast.DestinationIP)
	if broadcastDstIP == nil {
		broadcastDstIP = net.IPv4(255, 255, 255, 255)
		slog.Info("defaulting broadcast destination IP", "ip", broadcastDstIP)
	}

	broadcastSelfIP := net.ParseIP(cfg.Broadcast.SelfIP)
	if broadcastSelfIP == nil {
		slog.Error("invalid broadcast self IP", "self_ip", cfg.Broadcast.SelfIP)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// dial modbus tcp
	conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", cfg.Modbus.IP, cfg.Modbus.Port))
	if err != nil {
		slog.Error("failed to dial modbus tcp", "err", err)
		os.Exit(1)
	}
	defer conn.Close()

	q := solar.NewClient(modbus.NewModbusConn(conn, cfg.Modbus.SlaveID))
	go q.Run(ctx)

	// MQTT config etc
	if cfg.MQTT.ClientID == "" {
		cfg.MQTT.ClientID = "huawei-solar-go-agent"
	}

	mopts := mqtt.NewClientOptions().AddBroker(cfg.MQTT.Broker).SetClientID(cfg.MQTT.ClientID)
	if cfg.MQTT.Username != "" {
		mopts.SetUsername(cfg.MQTT.Username)
		mopts.SetPassword(cfg.MQTT.Password)
	}
	mopts.SetAutoReconnect(true).SetConnectRetry(true).SetConnectTimeout(5 * time.Second)

	mc := mqtt.NewClient(mopts)
	if token := mc.Connect(); !token.WaitTimeout(10*time.Second) || token.Error() != nil {
		slog.Error("mqtt connect", "err", token.Error())
		os.Exit(1)
	}
	defer mc.Disconnect(2000)

	// Querier<->MQTT messages
	dataCh := make(chan *solar.Data, 10)

	// Initial broadcast to let the inverter know we're here
	err = q.BroadcastHello(broadcastDstIP, broadcastSelfIP)
	if err != nil {
		slog.Warn("problem when trying to broadcast hello message, proceeding anyway (normal when across VLANs/subnets)", "err", err)
	}

	err = q.Login(ctx, cfg.Modbus.Username, cfg.Modbus.Password)
	if err != nil {
		slog.Warn("problem when trying to log in to inverter, proceeding anyway", "err", err)
	} else {
		slog.Info("logged in")
	}

	// Query goroutine
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if cfg.LogQuery {
					slog.Info("querying...")
				}
				d, err := q.Query(ctx)

				if err != nil {
					slog.Warn("query error", "err", err)
					slog.Info("attempting login again (likely timed out)")

					err = q.Login(ctx, cfg.Modbus.Username, cfg.Modbus.Password)
					if err != nil {
						slog.Warn("failed to complete login again", "err", err)
						slog.Info("sending broadcast then trying login again")

						err = q.BroadcastHello(broadcastDstIP, broadcastSelfIP)
						if err != nil {
							slog.Warn("failed to send broadcast again (normal across VLANs/subnets)", "err", err)
						} else {
							slog.Info("successfully sent broadcast")
						}

						slog.Info("attempting login again")
						err = q.Login(ctx, cfg.Modbus.Username, cfg.Modbus.Password)
						if err != nil {
							slog.Warn("failed to complete login again", "err", err)
						} else {
							slog.Info("successfully logged in again")
						}
					}

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
