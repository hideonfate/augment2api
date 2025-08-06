package config

import (
	"augment2api/pkg/logger"
	"os"
)

type Config struct {
	RedisConnString string
	AuthToken       string
	CodingMode      string
	CodingToken     string
	TenantURL       string
	AccessPwd       string
	RoutePrefix     string
	ProxyURL        string
	RemoveFree      string
	UserAgent       string
}

const version = "v1.0.9"

var AppConfig Config

func InitConfig() error {
	// 从环境变量读取配置
	AppConfig = Config{
		// 必填配置
		RedisConnString: getEnv("REDIS_CONN_STRING", ""),
		AccessPwd:       getEnv("ACCESS_PWD", ""),
		// 非必填配置
		AuthToken:   getEnv("AUTH_TOKEN", ""),   // api鉴权token
		RoutePrefix: getEnv("ROUTE_PREFIX", ""), // 自定义openai接口路由前缀
		CodingMode:  getEnv("CODING_MODE", "false"),
		CodingToken: getEnv("CODING_TOKEN", ""),
		TenantURL:   getEnv("TENANT_URL", ""),
		ProxyURL:    getEnv("PROXY_URL", ""),       // 代理URL配置
		RemoveFree:  getEnv("REMOVE_FREE", "true"), // 是否移除免费账户
		UserAgent:   getEnv("USER_AGENT", "Augment.vscode-augment/0.521.1 (darwin; arm64; 24.2.0) vscode/1.98.2"),
	}

	if AppConfig.CodingMode == "false" {

		// redis连接字符串 示例: redis://default:pwd@localhost:6379
		if AppConfig.RedisConnString == "" {
			logger.Log.Fatalln("未配置环境变量 REDIS_CONN_STRING")
		}

	}

	// 为了安全，必须配置访问密码
	if AppConfig.AccessPwd == "" {
		logger.Log.Fatalln("未配置环境变量 ACCESS_PWD")
	}

	logger.Log.Info("Welcome to use Augment2Api! Current Version: " + version)

	logger.Log.Info("Augment2Api配置加载完成:\n" +
		"----------------------------------------\n" +
		"AuthToken:    " + AppConfig.AuthToken + "\n" +
		"AccessPwd:    " + AppConfig.AccessPwd + "\n" +
		"RedisConnString: " + AppConfig.RedisConnString + "\n" +
		"RoutePrefix: " + AppConfig.RoutePrefix + "\n" +
		"ProxyURL: " + AppConfig.ProxyURL + "\n" +
		"RemoveFree: " + AppConfig.RemoveFree + "\n" +
		"----------------------------------------")

	logger.Log.Info("Everything is set up, now start to fully enjoy the charm of AI ！")

	return nil
}

func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}
