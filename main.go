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
	for i, acc := range accounts {
		wg.Add(1)
		go func(account request.Authentication, proxies []string) {
			defer wg.Done()
			handleAccount(account, proxies)
		}(acc, proxies)
	}

	wg.Wait()
}

func handleAccount(authInfo request.Authentication, proxies []string) {
	rand.Seed(time.Now().UnixNano())
	client := resty.New().
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
				return
			}
			lastLogin = time.Now()
		}

		// Rotate proxies for each request
		for _, proxyURL := range proxies {
			client.SetProxy(proxyURL)

			res, err := client.R().
				SetHeader("authorization", fmt.Sprintf("Bearer %v", loginResponse.Data.Token)).
				SetBody(keepAliveRequest).
				Post(constant.KeepAliveURL)
			if err != nil {
				logger.Error("Keep alive error", zap.String("acc", authInfo.Email), zap.Error(err))
			}

			res, err = client.R().
				SetHeader("authorization", fmt.Sprintf("Bearer %v", loginResponse.Data.Token)).
				Get(constant.GetPointURL)
			if err != nil {
				logger.Error("Get point error", zap.String("acc", authInfo.Email), zap.Error(err))
			} else {
				// Parse response to extract email and points
				var getPointResponse struct {
					Status  bool   `json:"status"`
					Message string `json:"message"`
					Data    struct {
						RewardPoint struct {
							Email  string  `json:"userId"`
							Points float64 `json:"points"`
						} `json:"rewardPoint"`
					} `json:"data"`
				}

				err = res.Unmarshal(&getPointResponse)
				if err != nil {
					logger.Error("Error parsing get point response", zap.Error(err))
				} else {
					logger.Info("Account points", zap.String("email", getPointResponse.Data.RewardPoint.Email), zap.Float64("points", getPointResponse.Data.RewardPoint.Points))
				}
			}

			time.Sleep(3 * time.Minute)
		}
	}
}
