package main

import (
	"fmt"
	"net"
	"os"
	"time"

	"gopkg.in/yaml.v3"
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

type LoadedConfig struct {
	Config

	interval time.Duration

	broadcastDstIP  net.IP
	broadcastSelfIP net.IP
}

func loadConfig(path string) (*LoadedConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg LoadedConfig
	if err := yaml.Unmarshal(b, &cfg.Config); err != nil {
		return nil, err
	}

	err = parseConfig(&cfg)
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}

func parseConfig(cfg *LoadedConfig) error {
	if cfg.MQTT.ClientID == "" {
		cfg.MQTT.ClientID = "huawei-solar-go-agent"
	}

	if cfg.Broadcast.DestinationIP == "" {
		cfg.Broadcast.DestinationIP = "255.255.255.255"
	}

	interval := 30 * time.Second
	if cfg.Interval != "" {
		if d, err := time.ParseDuration(cfg.Interval); err == nil {
			interval = d
		} else {
			return fmt.Errorf("invalid interval %q: %v", cfg.Interval, err)
		}
	}
	cfg.interval = interval

	cfg.broadcastDstIP = net.ParseIP(cfg.Broadcast.DestinationIP)
	cfg.broadcastSelfIP = net.ParseIP(cfg.Broadcast.SelfIP)
	if cfg.broadcastDstIP == nil || cfg.broadcastSelfIP == nil {
		return fmt.Errorf("invalid broadcast ip %q; %q", cfg.Broadcast.DestinationIP, cfg.Broadcast.SelfIP)
	}

	return nil
}
