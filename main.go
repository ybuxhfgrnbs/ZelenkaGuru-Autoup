package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
)

type Config struct {
	LztToken         string `json:"lzt_token"`
	TgBotToken       string `json:"tg_bot_token"`
	TgUserID         int    `json:"tg_user_id"`
	ThreadIDs        string `json:"thread_ids"`
	Delay            uint   `json:"delay_hours"`
	DelayDisabledApi uint   `json:"delay_if_api_disabled_minutes"`
}

type ApiZelenkaResponse struct {
	Status  string   `json:"status"`
	Message string   `json:"message"`
	Errors  []string `json:"errors"`
}

var config Config
var restyClient = resty.New()

var (
	successThreads     uint
	failedThreads      uint
	outOfAttempThreads uint
)

func initConfig() {
	if _, err := os.Stat("config.json"); err == nil {
		data, err := os.ReadFile("config.json")
		if err != nil {
			log.Fatalln("Не удалось прочитать файл:", err)
		}
		if err := json.Unmarshal(data, &config); err != nil {
			log.Fatalln("Ошибка при открытии файла:", err)
		}
		log.Println("Успешно загрузил конфиг")
	} else {
		config := Config{
			LztToken:         "",
			TgBotToken:       "",
			TgUserID:         123,
			ThreadIDs:        "123,123",
			Delay:            18,
			DelayDisabledApi: 60,
		}
		configJson, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			log.Fatalln("Ошибка преобразования в JSON:", err)
		}
		file, err := os.Create("config.json")
		if err != nil {
			log.Fatalln("Ошибка создания файла:", err)
		}
		defer file.Close()

		_, err = file.Write(configJson)
		if err != nil {
			log.Fatalln("Ошибка записи данных в файл:", err)
		}
		log.Fatalln("Заполните config.json")
	}

}

func checkLztApiStatus() {
	for {
		lztToken := config.LztToken
		url := "https://api.zelenka.guru/threads/recent?limit=1"

		req, err := restyClient.R().SetHeader("accept", "application/json").SetHeader("authorization", fmt.Sprintf("Bearer %s", lztToken)).Get(url)
		if err != nil {
			log.Println(err)
		}
		if string(req.Body()) == "The API is currently disabled." {
			delay := config.DelayDisabledApi
			log.Printf("API Zelenka выключенна сплю на %d минут...", delay)
			if len(config.TgBotToken) > 0 {
				message := fmt.Sprintf("API Zelenka выключенна сплю на %d минут...", delay)
				sendMessageTg(message)
			}
			delayDuration := time.Duration(delay) * time.Minute
			time.Sleep(delayDuration)
		} else {
			log.Println("API Zelenka", req.Status())
			time.Sleep(3 * time.Second)
			break
		}
	}
}

func handleStatusCode(s int, t string, attemp int, resp *ApiZelenkaResponse) {
	if s == 200 {
		log.Println(resp)
		time.Sleep(3 * time.Second)
		return
	} else if s == 429 {
		if attemp <= 3 {
			log.Printf("Пытаюсь апнуть тему zelenka.guru/threads/%s/ %d/%d", t, attemp, 3)
			time.Sleep(3 * time.Second)
			bumpReq(t, attemp+1)
		} else {
			log.Printf("Превышено колличество попыток для темы zelenka.guru/threads/%s/", t)
			outOfAttempThreads++
		}
	} else if s == 403 {
		log.Printf("Тема zelenka.guru/threads/%s/ удалена (или принадлежит не вам)", t)
		time.Sleep(3 * time.Second)
		failedThreads++
		return
	} else {
		attemp = 0
	}
}

func bumpReq(t string, attemp int) {
	url := fmt.Sprintf("https://api.zelenka.guru/threads/%s/bump", t)

	req, err := restyClient.R().SetHeader("accept", "application/json").SetHeader("authorization", fmt.Sprintf("Bearer %s", config.LztToken)).Post(url)
	if err != nil {
		log.Println("Ошибка при совершении запроса:", err)
	}
	var response ApiZelenkaResponse
	if err := json.Unmarshal(req.Body(), &response); err != nil {
		if req.StatusCode() == 429 {
			log.Printf("zelenka.guru/threads/%s/\n429 Too Many Requests", t)
		} else {
			log.Println("Ошибка при получении JSON:", err)
		}
	}
	if len(response.Errors) > 0 {
		log.Printf("Не удалось поднять тему zelenka.guru/threads/%s/\n%s", t, response.Errors)
		failedThreads++
		return
	} else if response.Status == "ok" {
		log.Printf("Поднял тему zelenka.guru/threads/%s/", t)
		successThreads++
	}
	handleStatusCode(req.StatusCode(), t, attemp, &response)
}

func bumpThreads() {
	threads := strings.Split(config.ThreadIDs, ",")
	for _, thread := range threads {
		bumpReq(thread, 1)
		time.Sleep(3 * time.Second)
	}
}

func sendMessageTg(m string) {
	token := config.TgBotToken
	chatId := config.TgUserID
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	payload := map[string]interface{}{
		"chat_id": chatId,
		"text":    m,
	}
	resp, err := restyClient.R().
		SetHeader("Content-Type", "application/json").
		SetQueryParam("parse_mode", "HTML").
		SetBody(payload).
		Post(url)

	if err != nil {
		log.Printf("Ошибка при выполнении запроса: %v\n", err)
		return
	}

	if resp.StatusCode() == 200 {
		log.Println("Сообщение в телеграм успешно отправлено")
	} else {
		log.Printf("Ошибка при отправке сообщения в телеграм: %v\n", resp)
	}
}

func main() {
	initConfig()
	for {
		failedThreads = 0
		successThreads = 0
		outOfAttempThreads = 0
		checkLztApiStatus()
		bumpThreads()
		log.Printf("\nУспешно закончил круг\nУспешно поднятых тем: %d\nНе удалось поднять тем: %d\nПревышенно количество попыток для тем: %d\nСледуюший круг через %d часов", successThreads, failedThreads, outOfAttempThreads, config.Delay)
		if len(config.TgBotToken) > 0 {
			message := fmt.Sprintf("<b>💚 Успешно закончил круг</b>\n\n<b>🎯 Успешно поднятых тем:</b> <code>%d</code>\n<b>💔 Не удалось поднять:</b> <code>%d</code>\n<b>☠️ Полученных ошибок от API:</b> <code>%d</code>\n\n<i>Следующий круг через <code>%d</code> часов.</i>", successThreads, failedThreads, outOfAttempThreads, config.Delay)
			sendMessageTg(message)
		}
		delayDuration := time.Duration(config.Delay) * time.Hour
		time.Sleep(delayDuration)
	}
}
