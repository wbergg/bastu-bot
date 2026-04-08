package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/wbergg/telegram"
)

type Target struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Sensor *int   `json:"sensor,omitempty"`
}

type Config struct {
	APIKey        string   `json:"apikey"`
	Channel       int64    `json:"channel"`
	LogFile       string   `json:"logfile"`
	MessageHeader string   `json:"message_header"`
	Targets       []Target `json:"targets"`
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("apikey is required in config")
	}
	if cfg.Channel == 0 {
		return nil, fmt.Errorf("channel is required in config")
	}
	if len(cfg.Targets) == 0 {
		return nil, fmt.Errorf("at least one target is required in config")
	}
	for i, t := range cfg.Targets {
		if t.Name == "" || t.URL == "" {
			return nil, fmt.Errorf("target %d: name and url are required", i)
		}
	}

	if cfg.MessageHeader == "" {
		cfg.MessageHeader = "Current BASTU temperature:"
	}

	return &cfg, nil
}

func fetchTemperature(target Target) (float64, error) {
	resp, err := http.Get(target.URL)
	if err != nil {
		return 0, fmt.Errorf("fetching %s: %w", target.URL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("reading response: %w", err)
	}

	if len(body) == 0 {
		return 0, fmt.Errorf("empty response from %s", target.URL)
	}

	var temp TempData
	if err := json.Unmarshal(body, &temp); err != nil {
		return 0, fmt.Errorf("parsing JSON: %w", err)
	}

	sensorIdx := 0
	if target.Sensor != nil {
		sensorIdx = *target.Sensor
	}

	if sensorIdx >= len(temp.Temperatures) {
		return 0, fmt.Errorf("sensor index %d not found (have %d sensors)", sensorIdx, len(temp.Temperatures))
	}

	return temp.Temperatures[sensorIdx].Temperature, nil
}

func setupLogging(logFile string) *os.File {
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
	})

	if logFile == "" {
		return nil
	}

	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("Failed to open log file %s: %v", logFile, err)
	}

	log.SetOutput(f)
	return f
}

type TempData struct {
	SensorCount  int `json:"sensor_count"`
	Temperatures []struct {
		Sensor      int     `json:"sensor"`
		Temperature float64 `json:"temperature"`
	} `json:"temperatures"`
}

func main() {
	configPath := flag.String("config", "config.json", "Path to config file")
	debugTelegram := flag.Bool("telegram-debug", false, "Turns on debug for telegram")
	debugStdout := flag.Bool("stdout", false, "Turns on stdout rather than sending to telegram")
	telegramTest := flag.Bool("telegram-test", false, "Sends a test message to specified telegram channel")
	flag.Parse()

	// Load config
	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Setup logging
	if f := setupLogging(cfg.LogFile); f != nil {
		defer f.Close()
	}

	log.Infof("Starting program with channel ID: %d", cfg.Channel)
	log.Infof("Loaded %d target(s) from config", len(cfg.Targets))

	// Initiate telegram
	tg := telegram.New(cfg.APIKey, cfg.Channel, *debugTelegram, *debugStdout)
	tg.Init(*debugTelegram)
	log.Info("Telegram client initialized")

	if *telegramTest {
		tg.SendM("DEBUG: bastubot test message")
		log.Info("Test message sent, exiting")
		os.Exit(0)
	}

	for {
		// Read messages from Telegram
		updates, err := tg.ReadM()
		if err != nil {
			log.Errorf("Can't read from Telegram: %v", err)
			log.Info("Retrying in 10 seconds...")
			time.Sleep(10 * time.Second)
			continue
		}

		// Loop
		log.Info("Entering main update loop")
		for update := range updates {
			if update.Message == nil { // ignore non-message updates
				continue
			}

			// Debug
			if *debugStdout {
				log.Infof("Received message from chat %d [%s]: %s", update.Message.Chat.ID, update.Message.Chat.Type, update.Message.Text)
			}

			if update.Message.IsCommand() {
				// Create switch to search for commands
				switch strings.ToLower(update.Message.Command()) {

				// Bastu case
				case "bastu", "sauna":
					message := update.Message.CommandArguments()

					if message == "" {
						// If nothing was inputted, return calling userid
						message = update.Message.From.UserName
						if message == "" {
							message = update.Message.From.FirstName
						}
					}

					var sb strings.Builder
					fmt.Fprintln(&sb, cfg.MessageHeader)
					for _, target := range cfg.Targets {
						temp, err := fetchTemperature(target)
						if err != nil {
							log.Errorf("Error fetching %s: %v", target.Name, err)
							fmt.Fprintf(&sb, "%s: error (%v)\n", target.Name, err)
							continue
						}
						fmt.Fprintf(&sb, "%s: %.2f°C\n", target.Name, temp)
					}
					tg.SendTo(update.Message.Chat.ID, sb.String())

				default:
					// Unknown command
					tg.SendM("")
				}
			}
		}

		log.Warn("Update channel closed, reconnecting in 10 seconds...")
		time.Sleep(10 * time.Second)
	}
}
