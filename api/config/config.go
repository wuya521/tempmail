package config

import (
	"os"
	"strconv"
	"strings"
)

func alipayKeyFromEnvOrFile(envKey, fileKey string) string {
	v := os.Getenv(envKey)
	if fp := strings.TrimSpace(os.Getenv(fileKey)); fp != "" {
		b, err := os.ReadFile(fp)
		if err != nil {
			return v
		}
		return string(b)
	}
	return v
}

type Config struct {
	Port          string
	DBDSN         string
	RedisAddr     string
	RedisPassword string
	RateLimit     int
	RateWindow    int // seconds
	SMTPServerIP  string // 仅从 SMTP_SERVER_IP 环境变量读取
	SMTPHostname  string // 邮件服务器场指向的 hostname，不硬编码
	ShopAssetDir    string // 收款码等静态文件目录
	InternalSecret  string // Postfix→API 内部通信密钥；空=跳过验证

	// 支付宝当面付（precreate + 异步通知）；密钥仅从环境变量读取，勿写入镜像或仓库
	AlipayAppID       string
	AlipayPrivateKey  string // PEM 或单行 Base64 正文，可用 \n 表示换行
	AlipayPublicKey   string // 支付宝公钥，用于验签异步通知
	AlipayNotifyURL   string // 须为外网可访问的 HTTPS，如 https://你的域名/public/alipay/notify
	AlipayGateway     string // 默认正式网关
}

// AlipayPrecreateConfigured 是否具备发起 precreate 的最小配置（不含店铺开关）
func (c *Config) AlipayPrecreateConfigured() bool {
	if c == nil {
		return false
	}
	return strings.TrimSpace(c.AlipayAppID) != "" &&
		strings.TrimSpace(c.AlipayPrivateKey) != "" &&
		strings.TrimSpace(c.AlipayPublicKey) != "" &&
		strings.TrimSpace(c.AlipayNotifyURL) != ""
}

func Load() *Config {
	rl, _ := strconv.Atoi(getEnv("RATE_LIMIT", "500"))
	rw, _ := strconv.Atoi(getEnv("RATE_WINDOW", "60"))

	return &Config{
		// ★ PORT：API 容器内监听端口，默认 8080。
		// 由 .env 中的 API_PORT 注入。修改此端口后需同步：
		//   1. .env / .env.example 的 API_PORT
		//   2. docker-compose.yml api.ports 右边数字
		//   3. nginx/default.conf 所有 proxy_pass http://api:8080
		//   4. postfix/entrypoint.sh curl http://api:8080
		//   5. postfix/mail-receiver.py API_URL 默认值
		Port: getEnv("PORT", "8080"),
		DBDSN: getEnv("DB_DSN", ""),
		// ★ RedisAddr：Redis 容器内部地址，格式 "host:port"。
		// 默认 "redis:6379"，"redis" 是 Docker 内部服务名，不需要修改。
		RedisAddr:     getEnv("REDIS_ADDR", "redis:6379"),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),
		RateLimit:     rl,
		RateWindow:    rw,
		SMTPServerIP:  os.Getenv("SMTP_SERVER_IP"),
		SMTPHostname:  os.Getenv("SMTP_HOSTNAME"),
		ShopAssetDir:    getEnv("SHOP_ASSET_DIR", "/data/shop"),
		InternalSecret:  os.Getenv("INTERNAL_SECRET"),

		AlipayAppID:      os.Getenv("ALIPAY_APP_ID"),
		AlipayPrivateKey: alipayKeyFromEnvOrFile("ALIPAY_PRIVATE_KEY", "ALIPAY_PRIVATE_KEY_FILE"),
		AlipayPublicKey:  alipayKeyFromEnvOrFile("ALIPAY_PUBLIC_KEY", "ALIPAY_PUBLIC_KEY_FILE"),
		AlipayNotifyURL:  strings.TrimSpace(os.Getenv("ALIPAY_NOTIFY_URL")),
		AlipayGateway:    getEnv("ALIPAY_GATEWAY", "https://openapi.alipay.com/gateway.do"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
