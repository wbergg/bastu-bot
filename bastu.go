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
	// Define the flag. The third argument is the default value.
	channel := flag.Int64("channel", 0, "Channel ID to be used")
	apikey := flag.String("apikey", "", "Bot API token to be used")
	debugTelegram := flag.Bool("telegram-debug", false, "Turns on debug for telegram")
	debugStdout := flag.Bool("stdout", false, "Turns on stdout rather than sending to telegram")
	telegramTest := flag.Bool("telegram-test", false, "Sends a test message to specified telegram channel")
	logFile := flag.String("logfile", "", "Path to log file (default: stdout)")
	flag.Parse()

	// Setup logging
	if f := setupLogging(*logFile); f != nil {
		defer f.Close()
	}

	log.Infof("Starting program with channel ID: %d", *channel)

	if *channel == 0 {
		log.Fatal("No channel ID provided. Exiting.")
	}
	if *apikey == "" {
		log.Fatal("No API key provided. Exiting.")
	}

	// Initiate telegram

	tg := telegram.New(*apikey, *channel, *debugTelegram, *debugStdout)
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

					// URL of the JSON API
					url := "http://192.168.1.137"

					// Send the GET request
					resp, err := http.Get(url)
					if err != nil {
						log.Errorf("Error fetching JSON from %s: %v", url, err)
						reply := fmt.Sprintf("Error fetching JSON: %v", err)
						tg.SendTo(update.Message.Chat.ID, reply)
						continue
					}

					// Read the response body
					body, err := io.ReadAll(resp.Body)
					resp.Body.Close()
					if err != nil {
						log.Errorf("Error reading response body: %v", err)
						reply := fmt.Sprintf("Error reading response body: %v", err)
						tg.SendTo(update.Message.Chat.ID, reply)
						continue
					}

					// Check for empty response
					if len(body) == 0 {
						log.Error("Received empty JSON response")
						reply := "Error: received empty JSON response"
						tg.SendTo(update.Message.Chat.ID, reply)
						continue
					}

					// Parse the JSON
					var temp TempData
					err = json.Unmarshal(body, &temp)
					if err != nil {
						log.Errorf("Error parsing JSON: %v, body: %s", err, string(body))
						reply := fmt.Sprintf("Error parsing JSON: %v", err)
						tg.SendTo(update.Message.Chat.ID, reply)
						continue
					}

					if len(temp.Temperatures) == 0 {
						log.Error("No temperature sensors in response")
						tg.SendTo(update.Message.Chat.ID, "Error: no temperature sensors available")
						continue
					}

					// Only read from sensor X
					reply := fmt.Sprintf("Current BASTU temperature: %.2f°C\n", temp.Temperatures[0].Temperature)
					tg.SendTo(update.Message.Chat.ID, reply)

					// Loop through all sensors
					//for _, t := range temp.Temperatures {
					//	reply := fmt.Sprintf("Sensor %d: %.2f°C\n", t.Sensor, t.Temperature)
					//	tg.SendTo(update.Message.Chat.ID, reply)
					//}

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
