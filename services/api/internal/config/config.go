package config

import (
	"errors"
	"flag"
	"net"
	"os"
	"path/filepath"
)

type Config struct {
	Host         string
	Port         string
	DataRoot     string
	WorkerCmd    string
	WorkerArgs   []string
	DesktopToken string
	Version      string
}

func Load() (Config, error) {
	var cfg Config
	flag.StringVar(&cfg.Host, "host", env("KAH_HOST", "127.0.0.1"), "listen host")
	flag.StringVar(&cfg.Port, "port", env("KAH_PORT", "48761"), "listen port")
	flag.StringVar(&cfg.DataRoot, "data-root", env("KAH_DATA_ROOT", ".run-data"), "data root")
	flag.StringVar(&cfg.WorkerCmd, "worker", env("KAH_WORKER_CMD", ""), "worker executable")
	flag.Parse()
	cfg.DesktopToken = os.Getenv("KAH_DESKTOP_TOKEN")
	cfg.Version = "0.1.0"

	if ip := net.ParseIP(cfg.Host); ip == nil || !ip.IsLoopback() {
		if os.Getenv("KAH_ALLOW_LAN") != "1" {
			return Config{}, errors.New("non-loopback binding requires KAH_ALLOW_LAN=1")
		}
	}
	abs, err := filepath.Abs(cfg.DataRoot)
	if err != nil {
		return Config{}, err
	}
	cfg.DataRoot = filepath.Clean(abs)
	if workerModule := os.Getenv("KAH_WORKER_MODULE"); workerModule != "" {
		if cfg.WorkerCmd == "" {
			cfg.WorkerCmd = env("KAH_PYTHON", "python")
		}
		cfg.WorkerArgs = []string{"-u", "-m", workerModule}
	}
	return cfg, nil
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
