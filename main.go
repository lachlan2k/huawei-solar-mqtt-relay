package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"gopkg.in/yaml.v3"

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
		log.Fatalf("load config: %v", err)
	}

	interval := 30 * time.Second
	if cfg.Interval != "" {
		if d, err := time.ParseDuration(cfg.Interval); err == nil {
			interval = d
		} else {
			log.Printf("invalid interval %q, using default 30s: %v", cfg.Interval, err)
		}
	}

	// Inverter client config etc
	broadcastDstIP := net.ParseIP(cfg.Broadcast.DestinationIP)
	if broadcastDstIP == nil {
		broadcastDstIP = net.IPv4(255, 255, 255, 255)
	}

	broadcastSelfIP := net.ParseIP(cfg.Broadcast.SelfIP)
	if broadcastSelfIP == nil {
		log.Fatalf("invalid broadcast self IP %q", cfg.Broadcast.SelfIP)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	q, err := solar.NewClient(cfg.Modbus.IP, cfg.Modbus.Port, byte(cfg.Modbus.SlaveID))
	if err != nil {
		log.Fatalf("modbus client: %v", err)
	}
	defer q.Close()

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
		log.Fatalf("mqtt connect: %v", token.Error())
	}
	defer mc.Disconnect(2000)

	// Querier<->MQTT messages
	dataCh := make(chan *solar.Data, 10)

	// Initial broadcast to let the inverter know we're here
	err = q.BroadcastHello(broadcastDstIP, broadcastSelfIP)
	if err != nil {
		log.Printf("Problem when trying to broadcast hello message, proceeding anyway (normal when across VLANs/subnets) %v\n", err)
	}

	err = q.Login(cfg.Modbus.Username, cfg.Modbus.Password)
	if err != nil {
		log.Printf("Problem when trying to log in to inverter, proceeding anyway... %v\n", err)
	} else {
		log.Println("Logged in")
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
				log.Println("querying...")
				d, err := q.Query(ctx)

				if err != nil {
					log.Printf("query error: %v", err)
					log.Println("attempting login again (probably timed out)")

					err = q.Login(cfg.Modbus.Username, cfg.Modbus.Password)
					if err != nil {
						log.Printf("failed to complete login again: %v\n", err)
						log.Println("sending broadcast then trying login again")

						err = q.BroadcastHello(broadcastDstIP, broadcastSelfIP)
						log.Printf("error result from sending broadcast again, doing another login: %v\n", err)

						err = q.Login(cfg.Modbus.Username, cfg.Modbus.Password)
						log.Printf("result from login attempt after broadcast (looping): %v\n", err)
					}

					continue
				}

				if cfg.LogQuery {
					log.Printf("query data: %v\n", d.Pretty())
				}

				select {
				case dataCh <- d:
				default:
					log.Printf("data channel full, dropping sample (mqtt client sad?)")
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
					log.Printf("marshal error: %v", err)
					continue
				}
				token := mc.Publish(cfg.MQTT.Topic, cfg.MQTT.QoS, cfg.MQTT.Retain, payload)
				if !token.WaitTimeout(5*time.Second) || token.Error() != nil {
					log.Printf("mqtt publish error: %v", token.Error())
				}
			}
		}
	}()

	// Block until signal
	<-ctx.Done()
	log.Printf("goodbye")
}
