package main

import (
	"encoding/json"
	"fmt"
	"blockmesh/constant"
	"blockmesh/request"
	"crypto/tls"
	"github.com/go-resty/resty/v2"
	"github.com/mattn/go-colorable"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"math/rand"
	"os"
	"sync"
	"time"
)

var logger *zap.Logger

func main() {
	// Setting up logging
	config := zap.NewDevelopmentEncoderConfig()
	config.EncodeLevel = zapcore.CapitalColorLevelEncoder
	logger = zap.New(zapcore.NewCore(
		zapcore.NewConsoleEncoder(config),
		zapcore.AddSync(colorable.NewColorableStdout()),
		zapcore.DebugLevel,
	))

	// Loading configuration from conf.toml
	viper.SetConfigFile("./conf.toml")
	err := viper.ReadInConfig()
	if err != nil {
		panic(fmt.Errorf("fatal error config file: %w", err))
	}

	// Getting proxy list from configuration
	proxies := viper.GetStringSlice("proxies.data")

	// Getting account list from configuration
	var accounts []request.Authentication
	err = viper.UnmarshalKey("data.auth", &accounts)
	if err != nil {
		logger.Error("Error unmarshalling config", zap.Error(err))
		return
	}

	var wg sync.WaitGroup

	// Distributing proxies among accounts
	proxiesPerAccount := len(proxies) / len(accounts)
	extraProxies := len(proxies) % len(accounts)

	proxyIndex := 0
	for _, acc := range accounts {
		proxiesForThisAccount := proxiesPerAccount
		if extraProxies > 0 {
			proxiesForThisAccount++
			extraProxies--
		}

		for i := 0; i < proxiesForThisAccount; i++ {
			wg.Add(1)
			go func(proxy string, account request.Authentication) {
				defer wg.Done()
				ping(proxy, account)
			}(proxies[proxyIndex], acc)
			proxyIndex++
		}
	}

	wg.Wait()
}

func ping(proxyURL string, authInfo request.Authentication) {
	rand.Seed(time.Now().UnixNano())
	client := resty.New().SetProxy(proxyURL).
		SetTLSClientConfig(&tls.Config{InsecureSkipVerify: true}).
		SetHeader("content-type", "application/json").
		SetHeader("origin", "chrome-extension://fpdkjdnhkakefebpekbdhillbhonfjjp").
		SetHeader("accept", "*/*").
		SetHeader("accept-language", "en-US,en;q=0.9").
		SetHeader("priority", "u=1, i").
		SetHeader("sec-fetch-dest", "empty").
		SetHeader("sec-fetch-mode", "cors").
		SetHeader("sec-fetch-site", "cross-site").
		SetHeader("user-agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/127.0.0.0 Safari/537.36")

	loginRequest := request.LoginRequest{
		Username: authInfo.Email,
		Password: authInfo.Password,
		Logindata: struct {
			V        string `json:"_v"`
			Datetime string `json:"datetime"`
		}{
			V:        "1.0.6",
			Datetime: time.Now().Format("2006-01-02 15:04:05"),
		},
	}

	var loginResponse request.LoginResponse
	_, err := client.R().
		SetBody(loginRequest).
		SetResult(&loginResponse).
		Post(constant.LoginURL)
	if err != nil {
		logger.Error("Login error", zap.String("acc", authInfo.Email), zap.Error(err))
		time.Sleep(1 * time.Minute)
		go ping(proxyURL, authInfo)
		return
	}
	lastLogin := time.Now()

	keepAliveRequest := map[string]interface{}{
		"username":     authInfo.Email,
		"extensionid":  "fpdkjdnhkakefebpekbdhillbhonfjjp",
		"numberoftabs": 0,
		"_v":           "1.0.6",
	}

	for {
		if time.Since(lastLogin) > 2*time.Hour {
			loginRequest.Logindata.Datetime = time.Now().Format("2006-01-02 15:04:05")
			_, err := client.R().
				SetBody(loginRequest).
				SetResult(&loginResponse).
				Post(constant.LoginURL)
			if err != nil {
				logger.Error("Login error", zap.String("acc", authInfo.Email), zap.Error(err))
				time.Sleep(1 * time.Minute)
				go ping(proxyURL, authInfo)
				return
			}
		}

		// Performing KeepAlive request
		res, err := client.R().
			SetHeader("authorization", fmt.Sprintf("Bearer %v", loginResponse.Data.Token)).
			SetBody(keepAliveRequest).
			Post(constant.KeepAliveURL)
		if err != nil {
			logger.Error("Keep alive error", zap.String("acc", authInfo.Email), zap.Error(err))
		}

		// Performing GetPoint request
		res, err = client.R().
			SetHeader("authorization", fmt.Sprintf("Bearer %v", loginResponse.Data.Token)).
			Get(constant.GetPointURL)
		if err != nil {
			logger.Error("Get point error", zap.String("acc", authInfo.Email), zap.Error(err))
		} else {
			// Logging full response and extracting required data
			extractAndLogPoints(authInfo.Email, res.String())
		}

		time.Sleep(3 * time.Minute)
	}
}

func extractAndLogPoints(email, response string) {
	// Data structure for parsing the response
	type ResponseData struct {
		Data struct {
			RewardPoint struct {
				Points        float64 `json:"points"`
				LastKeepAlive string  `json:"lastKeepAlive"`
			} `json:"rewardPoint"`
		} `json:"data"`
	}

	// Parsing the JSON response
	var data ResponseData
	if err := json.Unmarshal([]byte(response), &data); err != nil {
		logger.Error("Failed to parse response", zap.Error(err))
		return
	}

	// Logging the extracted data
	logger.Info("Get point success",
		zap.String("acc", email),
		zap.Float64("points", data.Data.RewardPoint.Points),
		zap.String("lastKeepAlive", data.Data.RewardPoint.LastKeepAlive),
	)

	// Write to file
	writeToFile(email, data.Data.RewardPoint.Points)
}

func writeToFile(email string, points float64) {
	file, err := os.OpenFile("account_points.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		logger.Error("Failed to open file", zap.Error(err))
		return
	}
	defer file.Close()

	entry := fmt.Sprintf("%s - %.2f\n", email, points)
	if _, err := file.WriteString(entry); err != nil {
		logger.Error("Failed to write to file", zap.Error(err))
	}
}
