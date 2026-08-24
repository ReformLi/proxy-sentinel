package alert

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DingTalk 钉钉群机器人通知器（webhook + 可选加签）
type DingTalk struct {
	webhook string
	secret  string
	client  *http.Client
}

// NewDingTalk 创建钉钉通知器；secret 为空表示未开启加签
func NewDingTalk(webhook, secret string) *DingTalk {
	return &DingTalk{
		webhook: strings.TrimSpace(webhook),
		secret:  strings.TrimSpace(secret),
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// Configured 是否已配置 webhook
func (d *DingTalk) Configured() bool { return d != nil && d.webhook != "" }

// dingResponse 钉钉机器人响应体
type dingResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

// Send 发送 markdown 消息
func (d *DingTalk) Send(title, markdownText string) error {
	if !d.Configured() {
		return fmt.Errorf("钉钉 webhook 未配置")
	}
	api, err := d.signedURL(time.Now())
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"msgtype":  "markdown",
		"markdown": map[string]string{"title": title, "text": markdownText},
	})
	if err != nil {
		return err
	}
	resp, err := d.client.Post(api, "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("请求钉钉 webhook 失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
	var dr dingResponse
	if err := json.Unmarshal(body, &dr); err != nil {
		return fmt.Errorf("解析钉钉响应失败（HTTP %d）: %s", resp.StatusCode, truncate(string(body), 200))
	}
	if dr.ErrCode != 0 {
		return fmt.Errorf("钉钉返回错误 %d: %s（常见原因：webhook 地址失效、加签 secret 不匹配、触发频率限制）", dr.ErrCode, dr.ErrMsg)
	}
	return nil
}

// signedURL 生成带时间戳与签名的请求地址（官方加签算法：HMAC-SHA256(timestamp+"\n"+secret)）
func (d *DingTalk) signedURL(now time.Time) (string, error) {
	if d.secret == "" {
		return d.webhook, nil
	}
	ts := strconv.FormatInt(now.UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(d.secret))
	mac.Write([]byte(ts + "\n" + d.secret))
	sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	sep := "?"
	if strings.Contains(d.webhook, "?") {
		sep = "&"
	}
	return fmt.Sprintf("%s%stimestamp=%s&sign=%s", d.webhook, sep, ts, url.QueryEscape(sign)), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
