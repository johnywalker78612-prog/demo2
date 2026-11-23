// notify.go
//
// Email aur SMS bhejne ke 2 simple functions. Sirf Go standard library, koi external package nahi.
//
//	SendEmail(toEmail, code)  -> Brevo API se email   (free: 300 email/day, card nahi chahiye)
//	SendSMS(toPhone, code)    -> textbee API se SMS   (free: 300 SMS/month, apna Android phone + SIM)
//
// Chalane se pehle notify.go ke saath wale folder me ".env" naam ki file banao:
//
//	BREVO_API_KEY=xkeysib-...                 (Brevo dashboard -> SMTP & API -> API Keys)
//	FROM_EMAIL=johnywalker78612@gmail.com     (Brevo me verify kiya hua sender email)
//	FROM_NAME=Ampli
//	TEXTBEE_API_KEY=...                       (textbee.dev dashboard -> API Keys)
//
// Program start hote hi ye file apne aap padh leta hai (loadEnv function).
//
// Test run:
//
//	go run notify.go someone@example.com +919876543210 482913
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	brevoURL   = "https://api.brevo.com/v3/smtp/email"
	textbeeURL = "https://api.textbee.dev/api/v1/gateway/send-sms"
)

// Ek hi HTTP client, 15 second timeout ke saath (taaki request kabhi atke nahi).
var httpClient = &http.Client{Timeout: 15 * time.Second}

// SendEmail diye gaye email address par code bhejta hai.
func SendEmail(toEmail, code string) error {
	apiKey := os.Getenv("BREVO_API_KEY")
	fromEmail := os.Getenv("FROM_EMAIL")
	fromName := os.Getenv("FROM_NAME")
	if apiKey == "" || fromEmail == "" {
		return fmt.Errorf("BREVO_API_KEY aur FROM_EMAIL set karna zaroori hai")
	}
	if fromName == "" {
		fromName = "Verification"
	}

	// Brevo ko jo JSON bhejna hai
	body := map[string]any{
		"sender":      map[string]string{"name": fromName, "email": fromEmail},
		"to":          []map[string]string{{"email": toEmail}},
		"subject":     "Your verification code",
		"textContent": "Your verification code is: " + code,
		"htmlContent": "<p>Your verification code is:</p><h2>" + code + "</h2>",
	}

	headers := map[string]string{"api-key": apiKey}
	return postJSON(brevoURL, headers, body)
}

// SendSMS diye gaye phone number par code ka text message bhejta hai.
// toPhone international format me hona chahiye, jaise +919876543210
func SendSMS(toPhone, code string) error {
	apiKey := os.Getenv("TEXTBEE_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("TEXTBEE_API_KEY set karna zaroori hai")
	}

	body := map[string]any{
		"recipients": []string{toPhone},
		"message":    "Your verification code is: " + code,
	}

	headers := map[string]string{"x-api-key": apiKey}
	return postJSON(textbeeURL, headers, body)
}

// postJSON dono functions ka common helper hai:
// body ko JSON banata hai, POST karta hai, aur 2xx na aaye to error deta hai.
func postJSON(url string, headers map[string]string, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("json banane me error: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("request banane me error: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request bhejne me error: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("service ne error diya (status %d): %s", resp.StatusCode, respBody)
	}
	return nil
}

// loadEnv ".env" file padhkar uski values environment variables me daal deta hai.
// File ko notify.go wale folder me dhoondhta hai, na mile to current folder me.
// Agar koi variable terminal me pehle se set hai, to use nahi badalta.
func loadEnv() error {
	paths := []string{".env"}
	if _, thisFile, _, ok := runtime.Caller(0); ok {
		// thisFile = notify.go ka poora path
		paths = append([]string{filepath.Join(filepath.Dir(thisFile), ".env")}, paths...)
	}

	var data []byte
	var err error
	for _, p := range paths {
		data, err = os.ReadFile(p)
		if err == nil {
			break
		}
	}
	if err != nil {
		return fmt.Errorf(".env file nahi mili (notify.go ke saath wale folder me banao)")
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue // khaali line ya comment
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`) // quotes hata do
		if os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}
	return nil
}

func main() {
	if err := loadEnv(); err != nil {
		fmt.Println("Warning:", err)
	}

	if len(os.Args) != 4 {
		fmt.Println("Usage: go run notify.go <email> <phone> <code>")
		os.Exit(1)
	}
	email, phone, code := os.Args[1], os.Args[2], os.Args[3]

	if err := SendEmail(email, code); err != nil {
		fmt.Println("Email fail:", err)
	} else {
		fmt.Println("Email bhej diya:", email)
	}

	if err := SendSMS(phone, code); err != nil {
		fmt.Println("SMS fail:", err)
	} else {
		fmt.Println("SMS bhej diya:", phone)
	}
}
