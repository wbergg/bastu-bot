package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/wbergg/telegram"
)

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
	flag.Parse()

	// Use the channel variable
	fmt.Printf("Starting program with channel ID: %d\n", *channel)

	// Example usage
	if *channel == 0 {
		fmt.Println("No channel ID provided. Exiting.")
		return
	}
	if *apikey == "" {
		fmt.Println("No API key provided. Exiting.")
		return
	}

	// Initiate telegram

	tg := telegram.New(*apikey, *channel, *debugTelegram, *debugStdout)
	tg.Init(*debugTelegram)

	if *telegramTest {
		tg.SendM("DEBUG: bastubot test message")
		os.Exit(0)
	}

	// Read messages from Telegram
	updates, err := tg.ReadM()
	if err != nil {
		log.Error(err)
		panic("Cant read from Telegram")
	}

	// Loop
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
					// If nothings wa inpuuted, return calling userid
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
					reply := fmt.Sprintf("Error fetching JSON: %v", err)
					tg.SendTo(update.Message.Chat.ID, reply)
					return
				}
				defer resp.Body.Close()

				// Read the response body
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					reply := fmt.Sprintf("Error reading response body: %v", err)
					tg.SendTo(update.Message.Chat.ID, reply)
					return
				}

				// Check for empty response
				if len(body) == 0 {
					reply := "Error: received empty JSON response"
					tg.SendTo(update.Message.Chat.ID, reply)
					return
				}

				// Parse the JSON
				var temp TempData
				err = json.Unmarshal(body, &temp)
				if err != nil {
					reply := fmt.Sprintf("Error parsing JSON: %v", err)
					tg.SendTo(update.Message.Chat.ID, reply)
					return
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

}
