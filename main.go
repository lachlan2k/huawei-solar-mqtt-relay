package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
)

func main() {
	if os.Getenv("DEBUG") != "" {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	switch cmd {
	case "agent":
		fs := flag.NewFlagSet("agent", flag.ExitOnError)
		cfgPath := fs.String("config", "config.yaml", "Path to YAML config file")
		_ = fs.Parse(os.Args[2:])

		cfg, err := loadConfig(*cfgPath)
		if err != nil {
			slog.Error("load config", "err", err)
			os.Exit(1)
		}

		runAgent(cfg)
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
