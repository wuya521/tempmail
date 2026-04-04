package alipay

import (
	_ "time/tzdata" // Alpine 等环境 Asia/Shanghai
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

func formatAlipayTimestamp() string {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	return time.Now().In(loc).Format("2006-01-02 15:04:05")
}

const methodPrecreate = "alipay.trade.precreate"

// Client 支付宝开放平台网关客户端（RSA2）
type Client struct {
	AppID      string
	Gateway    string
	PrivateKey *rsa.PrivateKey
	PublicKey  *rsa.PublicKey // 支付宝公钥，用于验签异步通知
}

// Precreate 当面付预下单，返回 qr_code（用户主扫）
func (c *Client) Precreate(notifyURL, outTradeNo, subject, totalAmount string) (qrCode string, err error) {
	if c == nil || c.PrivateKey == nil {
		return "", fmt.Errorf("alipay client not configured")
	}
	biz := map[string]string{
		"out_trade_no":   outTradeNo,
		"total_amount":   totalAmount,
		"subject":        subject,
		"product_code":   "FACE_TO_FACE_PAYMENT",
	}
	bizJSON, err := json.Marshal(biz)
	if err != nil {
		return "", err
	}
	params := map[string]string{
		"app_id":      c.AppID,
		"method":      methodPrecreate,
		"charset":     "utf-8",
		"sign_type":   "RSA2",
		"timestamp":   formatAlipayTimestamp(),
		"version":     "1.0",
		"biz_content": string(bizJSON),
	}
	if strings.TrimSpace(notifyURL) != "" {
		params["notify_url"] = notifyURL
	}
	sign, err := signRSA2(params, c.PrivateKey)
	if err != nil {
		return "", err
	}
	params["sign"] = sign

	body := encodeForm(params)
	req, err := http.NewRequest(http.MethodPost, c.Gateway, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var outer map[string]json.RawMessage
	if err := json.Unmarshal(raw, &outer); err != nil {
		return "", fmt.Errorf("alipay response json: %w", err)
	}
	innerKey := "alipay_trade_precreate_response"
	innerRaw, ok := outer[innerKey]
	if !ok {
		return "", fmt.Errorf("alipay missing %s", innerKey)
	}
	var inner struct {
		Code    string `json:"code"`
		Msg     string `json:"msg"`
		SubCode string `json:"sub_code"`
		SubMsg  string `json:"sub_msg"`
		QRCode  string `json:"qr_code"`
	}
	if err := json.Unmarshal(innerRaw, &inner); err != nil {
		return "", err
	}
	if inner.Code != "10000" {
		if inner.SubMsg != "" {
			return "", fmt.Errorf("alipay %s: %s (%s)", inner.Msg, inner.SubCode, inner.SubMsg)
		}
		return "", fmt.Errorf("alipay %s", inner.Msg)
	}
	if inner.QRCode == "" {
		return "", fmt.Errorf("alipay empty qr_code")
	}
	return inner.QRCode, nil
}

// VerifyNotify 校验支付宝异步通知 RSA2 签名（params 含 sign、sign_type）
func (c *Client) VerifyNotify(params map[string]string) error {
	if c == nil || c.PublicKey == nil {
		return fmt.Errorf("alipay public key not configured")
	}
	sigB64 := params["sign"]
	if sigB64 == "" {
		return fmt.Errorf("missing sign")
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("sign base64: %w", err)
	}
	signType := params["sign_type"]
	if signType != "" && signType != "RSA2" {
		return fmt.Errorf("unsupported sign_type %s", signType)
	}
	content := buildSignContent(params)
	h := sha256.Sum256([]byte(content))
	if err := rsa.VerifyPKCS1v15(c.PublicKey, crypto.SHA256, h[:], sig); err != nil {
		return fmt.Errorf("verify sign: %w", err)
	}
	return nil
}

func signRSA2(params map[string]string, key *rsa.PrivateKey) (string, error) {
	// 拷贝并去掉 sign
	p := make(map[string]string, len(params))
	for k, v := range params {
		if k == "sign" || k == "sign_type" {
			continue
		}
		p[k] = v
	}
	content := buildSignContentFromMap(p)
	h := sha256.Sum256([]byte(content))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

func buildSignContent(params map[string]string) string {
	p := make(map[string]string, len(params))
	for k, v := range params {
		if k == "sign" || k == "sign_type" {
			continue
		}
		if strings.TrimSpace(v) == "" {
			continue
		}
		p[k] = v
	}
	return buildSignContentFromMap(p)
}

func buildSignContentFromMap(p map[string]string) string {
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(p[k])
	}
	return b.String()
}

func encodeForm(params map[string]string) string {
	vals := url.Values{}
	for k, v := range params {
		vals.Set(k, v)
	}
	return vals.Encode()
}

// normalizeKeyMaterial 去掉 BOM、\r（Windows 换行进 .env 时会导致 PEM/base64 解析失败）
func normalizeKeyMaterial(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "\ufeff")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "")
	return strings.TrimSpace(s)
}

// ParsePrivateKey 支持 PEM 整段或仅 Base64 正文（支付宝控制台常见格式）
func ParsePrivateKey(raw string) (*rsa.PrivateKey, error) {
	raw = normalizeKeyMaterial(raw)
	raw = strings.ReplaceAll(raw, "\\n", "\n")
	pemBytes := []byte(raw)
	if !strings.Contains(raw, "BEGIN") {
		pemBytes = ensurePEM(raw, "PRIVATE KEY")
	}
	if key, err := x509.ParsePKCS8PrivateKey(pemBytes); err == nil {
		if rk, ok := key.(*rsa.PrivateKey); ok {
			return rk, nil
		}
		return nil, fmt.Errorf("private key is not RSA")
	}
	if !strings.Contains(raw, "BEGIN") {
		pemBytes = ensurePEM(raw, "RSA PRIVATE KEY")
	} else {
		pemBytes = []byte(raw)
	}
	key, err := x509.ParsePKCS1PrivateKey(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	return key, nil
}

// ParsePublicKey 支付宝公钥（PEM 或 Base64 正文）
func ParsePublicKey(raw string) (*rsa.PublicKey, error) {
	raw = normalizeKeyMaterial(raw)
	raw = strings.ReplaceAll(raw, "\\n", "\n")
	block := []byte(raw)
	if !strings.Contains(raw, "BEGIN") {
		block = ensurePEM(raw, "PUBLIC KEY")
	}
	pub, err := x509.ParsePKIXPublicKey(block)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	rk, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key is not RSA")
	}
	return rk, nil
}

func ensurePEM(raw, blockType string) []byte {
	if strings.Contains(raw, "BEGIN") {
		return []byte(raw)
	}
	b64 := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(raw, " ", ""), "\n", ""), "\r", "")
	var lines []string
	for i := 0; i < len(b64); i += 64 {
		end := i + 64
		if end > len(b64) {
			end = len(b64)
		}
		lines = append(lines, b64[i:end])
	}
	return []byte(fmt.Sprintf("-----BEGIN %s-----\n%s\n-----END %s-----\n",
		blockType, strings.Join(lines, "\n"), blockType))
}
