package app

import (
	"os"
	"strings"
	"testing"
)

func TestEmailLogoTemplate(t *testing.T) {
	d := emailData{Subject: "Private Records: your verification code", Name: "Example Person", Intro: "You requested a security code.", PassportEnding: "1234", Code: "000000", Explanation: "Use this code to verify your email address. This preview code is not active.", URL: "http://localhost:8081/", Action: "Open Private Records", Closing: "If you did not request this code, you can ignore this email."}
	_, withoutLogo, err := renderEmail(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(withoutLogo, "<img") {
		t.Fatal("empty logo should not render a broken image")
	}
	if filename := os.Getenv("PRIVATE_EMAIL_PREVIEW"); filename != "" {
		if err := os.WriteFile(filename, []byte(withoutLogo), 0600); err != nil {
			t.Fatal(err)
		}
	}
	d.LogoURL = "https://assets.example.com/logo.png"
	_, html, err := renderEmail(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, `src="https://assets.example.com/logo.png"`) {
		t.Fatal("logo source missing")
	}
	for _, value := range []string{"javascript:alert(1)", "http://remote.example.com/logo.png", "https://user:secret@example.com/logo.png"} {
		cfg := Config{EmailLogoURL: value}
		if validateMailConfig(&cfg) == nil {
			t.Fatalf("accepted unsafe logo URL %q", value)
		}
	}
}
