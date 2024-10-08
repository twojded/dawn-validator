package main

import (
	"blockmesh/constant"
	"blockmesh/request"
	"crypto/tls"
	"fmt"
	"github.com/go-resty/resty/v2"
	"github.com/mattn/go-colorable"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"math/rand"
	"sync"
	"time"
)

var logger *zap.Logger

func main() {
	config := zap.NewDevelopmentEncoderConfig()
	config.EncodeLevel = zapcore.CapitalColorLevelEncoder
	logger = zap.New(zapcore.NewCore(
		zapcore.NewConsoleEncoder(config),
		zapcore.AddSync(colorable.NewColorableStdout()),
		zapcore.DebugLevel,
	))

	viper.SetConfigFile("./conf.toml")
	err := viper.ReadInConfig() // Find and read the config file
	if err != nil {             // Handle errors reading the config file
		panic(fmt.Errorf("fatal error config file: %w", err))
	}

	proxies := viper.GetStringSlice("proxies.data")

	var accounts []request.Authentication
	err = viper.UnmarshalKey("data.auth", &accounts)
	if err != nil {
		logger.Error("Error unmarshalling config: %v\n", zap.Error(err))
		return
	}

	var wg sync.WaitGroup
	for _, acc := range accounts {
		for i := 0; i < len(proxies); i++ {
			wg.Add(1)
			go func(proxy string, account request.Authentication) {
				defer wg.Done()
				handleAccount(proxy, account)
			}(proxies[i], acc)
		}
	}

	wg.Wait()
}

func handleAccount(proxyURL string, authInfo request.Authentication) {
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
		handleAccount(proxyURL, authInfo)
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
		if time.Now().Sub(lastLogin) > 2*time.Hour {
			loginRequest.Logindata.Datetime = time.Now().Format("2006-01-02 15:04:05")
			_, err := client.R().
				SetBody(loginRequest).
				SetResult(&loginResponse).
				Post(constant.LoginURL)
			if err != nil {
				logger.Error("Login error", zap.String("acc", authInfo.Email), zap.Error(err))
				time.Sleep(1 * time.Minute)
				handleAccount(proxyURL, authInfo)
				return
			}
			lastLogin = time.Now()
		}

		res, err := client.R().
			SetHeader("authorization", fmt.Sprintf("Bearer %v", loginResponse.Data.Token)).
			SetBody(keepAliveRequest).
			Post(constant.KeepAliveURL)
		if err != nil {
			logger.Error("Keep alive error", zap.String("acc", authInfo.Email), zap.Error(err))
		} else {
			logger.Info("Keep alive success", zap.String("acc", authInfo.Email), zap.String("points", extractPoints(res.String())))
		}

		time.Sleep(3 * time.Minute)
	}
}

func extractPoints(response string) string {
	// Function to parse and extract the "points" from the response string
	// Implement the logic based on the response format
	return response // Placeholder: Replace with actual extraction logic
}
