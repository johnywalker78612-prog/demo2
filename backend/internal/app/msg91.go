package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func msg91Sender() func(context.Context, string, string) error {
	if os.Getenv("MSG91_OTP_ENABLED") != "true" {
		return nil
	}
	key := strings.TrimSpace(os.Getenv("MSG91_AUTH_KEY"))
	template := strings.TrimSpace(os.Getenv("MSG91_OTP_TEMPLATE_ID"))
	if key == "" || template == "" {
		return nil
	}
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return func(ctx context.Context, phone, code string) error {
		return sendMSG91OTP(ctx, client, key, template, phone, code)
	}
}

func sendMSG91OTP(ctx context.Context, client *http.Client, key, template, phone, code string) error {
	params := url.Values{
		"mobile":      {strings.TrimPrefix(phone, "+")},
		"template_id": {template},
		"otp":         {code},
		"otp_length":  {"6"},
		"otp_expiry":  {"10"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://control.msg91.com/api/v5/otp?"+params.Encode(), strings.NewReader("{}"))
	if err != nil {
		return errors.New("could not build MSG91 request")
	}
	req.Header.Set("authkey", key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("MSG91 request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("MSG91 returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&result); err != nil || result.Type != "success" || result.RequestID == "" {
		return errors.New("MSG91 did not accept the SMS request")
	}
	return nil
}
