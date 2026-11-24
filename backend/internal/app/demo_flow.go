package app

import (
	"bytes"
	"html/template"
	"net/http"
	"private-records/web"
	"regexp"
	"strings"
	"time"
)

type demoSession struct {
	Passport, PersonID, Challenge, LookupNonce string
	Verified                                   bool
	CodeExpires                                time.Time
	CodeAttempts                               int
	CodeRecipient, CodeChannel                 string
	Expires                                    time.Time
}

func (s *Server) demoFlow(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	c, _ := r.Cookie("demo_session")
	key := ""
	if c != nil {
		key = hash(c.Value)
	}
	s.demoMu.Lock()
	defer s.demoMu.Unlock()
	se, ok := s.demoSessions[key]
	if !ok || !time.Now().Before(se.Expires) {
		if r.URL.Path != "/gov/passport" {
			http.Redirect(w, r, "/gov/passport", 303)
			return
		}
		t := randomToken("")
		key = hash(t)
		se = demoSession{Expires: time.Now().Add(20 * time.Minute)}
		s.demoSessions[key] = se
		http.SetCookie(w, &http.Cookie{Name: "demo_session", Value: t, Path: "/gov", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.cfg.SecureCookies, MaxAge: 1200})
	}
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
	}
	demoError := ""
	deliveryOptions, codeDestination, codeChannel := "", "", ""
	defaultDelivery := ""
	responseStatus := http.StatusOK
	switch r.URL.Path {
	case "/gov/status/pdf", "/gov/share-preview/pdf", "/gov/share-result/pdf":
		if !se.Verified || se.PersonID == "" {
			http.Redirect(w, r, "/gov/passport", http.StatusSeeOther)
			return
		}
		p, exists := s.store.snapshot().People[se.PersonID]
		if !exists {
			http.Redirect(w, r, "/gov/passport", http.StatusSeeOther)
			return
		}
		var pdf []byte
		var err error
		filename := "status.pdf"
		if r.URL.Path == "/gov/share-result/pdf" {
			pdf, err = demoSharePDF(p)
			filename = "share-code.pdf"
		} else {
			var photo *Photo
			if stored, hasPhoto := s.store.snapshot().Photos[p.ID]; hasPhoto {
				photo = &stored
			}
			pdf, err = demoStatusPDF(p, photo)
		}
		if err != nil {
			http.Error(w, "Could not create PDF", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(pdf)
		return
	case "/gov/passport":
		if r.Method == http.MethodPost {
			se.Passport = strings.TrimSpace(r.FormValue("documentNumber"))
			se.PersonID = ""
			se.Challenge = ""
			se.Verified = false
			s.demoSessions[key] = se
			http.Redirect(w, r, "/gov/date-of-birth", 303)
			return
		}
	case "/gov/date-of-birth":
		if r.Method == http.MethodPost {
			id := strings.TrimSpace(r.FormValue("documentNumber"))
			if id == "" {
				id = se.Passport
			}
			se.PersonID = ""
			se.Challenge = ""
			se.Verified = false
			dob := strings.TrimSpace(r.FormValue("date_of_birth"))
			for pid, p := range s.store.snapshot().People {
				if p.DateOfBirth == dob && (strings.EqualFold(p.PassportNumber, id) || p.ID == id) {
					se.PersonID = pid
					se.LookupNonce = randomToken("")
					break
				}
			}
			s.demoSessions[key] = se
			if se.PersonID == "" {
				renderDemoIdentityMismatch(w, id, dob)
				return
			}
			http.Redirect(w, r, "/gov/security-code-delivery", 303)
			return
		}

	case "/gov/security-code-delivery":
		p, exists := s.store.snapshot().People[se.PersonID]
		if !exists {
			http.Redirect(w, r, "/gov/passport", http.StatusSeeOther)
			return
		}

		// Check whether an administrator-generated OTP exists
		// for this particular client.
		adminCodeAvailable := s.hasAdminOTP(p.ID)

		if adminCodeAvailable {
			defaultDelivery = "admin"

			deliveryOptions = `<div class="osmuk-radios__item">
            <input class="osmuk-radios__input"
                type="radio"
                id="delivery-admin"
                name="delivery"
                value="admin"
                required
                checked>
            <label class="osmuk-label osmuk-radios__label"
                for="delivery-admin">
                Use a code from your administrator
            </label>
        </div>`
		}

		// Email and SMS are optional.
		// They should only be enabled when an actual sender is configured.
		for _, channel := range []string{"email", "sms"} {
			recipient := demoOTPRecipient(p, channel)

			if recipient == "" {
				continue
			}

			available := s.demoOTPSender(channel) != nil

			selected := available && defaultDelivery == ""

			if selected {
				defaultDelivery = channel
			}

			deliveryOptions += demoOTPOption(
				channel,
				recipient,
				available,
				selected,
			)
		}

		if deliveryOptions == "" {
			deliveryOptions = `<p class="osmuk-body">
            No administrator-generated code or delivery method
            is currently available for this record.
        </p>`
		}

		if r.Method == http.MethodPost {
			choice := strings.TrimSpace(r.PostFormValue("delivery"))

			// ADMIN OTP FLOW
			if choice == "admin" {
				if !adminCodeAvailable {
					responseStatus = http.StatusUnprocessableEntity
					demoError = "No valid administrator code is available for this record."
					break
				}

				se.CodeChannel = "admin"
				se.CodeRecipient = ""
				se.Challenge = ""
				se.CodeExpires = time.Time{}
				se.Verified = false

				s.demoSessions[key] = se

				http.Redirect(
					w,
					r,
					"/gov/security-code",
					http.StatusSeeOther,
				)
				return
			}

			// EMAIL / SMS FLOW
			recipient := demoOTPRecipient(p, choice)

			if recipient == "" {
				responseStatus = http.StatusUnprocessableEntity
				demoError = "Choose a valid email address or mobile number."
				break
			}

			sender := s.demoOTPSender(choice)

			if sender == nil {
				responseStatus = http.StatusServiceUnavailable
				demoError = "Delivery through that channel is unavailable. Choose another option."
				break
			}

			now := time.Now()

			if now.Before(s.demoSMSNext[p.ID]) {
				w.Header().Set("Retry-After", "60")
				http.Error(
					w,
					"Please wait before requesting another security code.",
					http.StatusTooManyRequests,
				)
				return
			}

			s.demoSMSNext[p.ID] = now.Add(time.Minute)

			s.demoMu.Unlock()

			nextSession, err := sendDemoOTP(
				r.Context(),
				sender,
				se,
				key,
				recipient,
			)

			s.demoMu.Lock()

			if err != nil {
				responseStatus = http.StatusBadGateway
				demoError = "The security code could not be sent. Please try again later."
				break
			}

			if s.demoSessions[key] != se {
				http.Redirect(w, r, "/gov/passport", http.StatusSeeOther)
				return
			}

			se = nextSession
			se.CodeChannel = choice
			s.demoSessions[key] = se

			http.Redirect(
				w,
				r,
				"/gov/security-code",
				http.StatusSeeOther,
			)
			return
		}

	case "/gov/security-code":
		if se.Verified {
			http.Redirect(w, r, "/gov/status", http.StatusSeeOther)
			return
		}

		if se.CodeChannel == "admin" {

			codeChannel = "admin"
			codeDestination = "your administrator"

			if r.Method == http.MethodPost {

				code := strings.TrimSpace(
					r.PostFormValue("verificationCode"),
				)

				// Verify the administrator-generated OTP.
				verified, err := s.verifyAdminOTP(
					se.PersonID,
					code,
				)

				if err != nil || !verified {
					demoError = "Invalid, expired, or already used administrator code."
					break
				}

				se.Verified = true
				se.Challenge = ""

				s.demoSessions[key] = se

				http.Redirect(
					w,
					r,
					"/gov/status",
					http.StatusSeeOther,
				)
				return
			}

			// Render administrator OTP entry page.
			break
		}

		p, exists := s.store.snapshot().People[se.PersonID]
		if !exists || se.CodeRecipient == "" || se.CodeRecipient != demoOTPRecipient(p, se.CodeChannel) {
			http.Redirect(w, r, "/gov/passport", http.StatusSeeOther)
			return
		}
		canUseAdminCode := se.CodeChannel == "email" && !s.cfg.SMTP.enabled() && s.hasAdminOTP(se.PersonID)
		if (se.Challenge == "" || !time.Now().Before(se.CodeExpires)) && !canUseAdminCode {
			http.Redirect(w, r, "/gov/security-code-delivery", http.StatusSeeOther)
			return
		}
		codeDestination = maskDemoOTPRecipient(se.CodeChannel, se.CodeRecipient)
		codeChannel = se.CodeChannel
		if r.Method == http.MethodPost {
			code := strings.TrimSpace(r.PostFormValue("verificationCode"))
			verified := se.verifyDemoOTP(key, code, time.Now())
			if !verified && canUseAdminCode {
				verified, _ = s.verifyAdminOTP(se.PersonID, code)
			}
			if !verified {
				demoError = "Invalid or expired security code."
				s.demoSessions[key] = se
				break
			}
			se.Challenge = ""
			se.Verified = true
			s.demoSessions[key] = se
			http.Redirect(w, r, "/gov/status", http.StatusSeeOther)
			return
		}
	case "/gov/photo":
		if !se.Verified {
			http.NotFound(w, r)
			return
		}
		r.SetPathValue("id", se.PersonID)
		s.getPhoto(w, r)
		return
	}
	content, _ := web.Files.ReadFile("pages/demo/" + strings.TrimPrefix(r.URL.Path, "/gov/") + ".html")
	// The original static preview forbids all network requests. This flow now
	// posts the existing form through the same-origin endpoint, so relax only
	// those two CSP directives in the response; no visual markup is changed.
	content = []byte(strings.ReplaceAll(string(content), "connect-src 'none'", "connect-src 'self'"))
	content = []byte(strings.ReplaceAll(string(content), "form-action 'none'", "form-action 'self'"))
	if se.Verified && se.PersonID != "" {
		p, exists := s.store.snapshot().People[se.PersonID]
		if exists {
			content = populateDemo(content, p)
			if r.URL.Path == "/gov/status" || r.URL.Path == "/gov/share-preview" || r.URL.Path == "/gov/share-result" {
				pdfPath := "/gov/status/pdf"
				if r.URL.Path == "/gov/share-result" {
					pdfPath = "/gov/share-result/pdf"
				}
				content = wireDemoPDFLinks(content, pdfPath)
			}
		}
	}

	if r.URL.Path == "/gov/security-code-delivery" {
		buttonState := "disabled"

		if defaultDelivery != "" {
			buttonState = ""
		}

		content = bytes.ReplaceAll(
			content,
			[]byte(`data-otp-submit`),
			[]byte(buttonState),
		)

		if demoError != "" {
			deliveryOptions = `<p class="osmuk-body" role="alert">` +
				template.HTMLEscapeString(demoError) +
				"</p>" + deliveryOptions
		}

		content = bytes.ReplaceAll(
			content,
			[]byte("<!-- OTP_DELIVERY_OPTIONS -->"),
			[]byte(deliveryOptions),
		)
	}

	if r.URL.Path == "/gov/security-code" {
		if codeChannel == "admin" {
			content = bytes.ReplaceAll(content, []byte("We requested a single-use, 6-digit security code for:"), []byte("Enter the single-use code provided by:"))
			content = bytes.ReplaceAll(content, []byte("It may take a few minutes to arrive. You need to use this code within 10 minutes or it will expire."), []byte("Administrator codes expire 24 hours after they are generated."))
			content = bytes.ReplaceAll(content, []byte(`<form action="/gov/security-code-delivery" method="post">`), []byte(`<form action="/gov/security-code-delivery" method="post" hidden>`))
		}
		if codeChannel != "admin" && s.hasAdminOTP(se.PersonID) {
			content = bytes.ReplaceAll(content, []byte("<!-- OTP_ADMIN_SWITCH -->"), []byte(`<form action="/gov/security-code-delivery" method="post"><button class="osmuk-button osmuk-button--secondary" type="submit" name="delivery" value="admin">Use a code from your administrator</button></form>`))
		}
		if codeChannel == "email" && s.cfg.LocalOTPOutbox && !s.cfg.SMTP.enabled() {
			if s.hasAdminOTP(se.PersonID) {
				content = bytes.ReplaceAll(content, []byte("<!-- OTP_ADMIN_INLINE_NOTICE -->"), []byte(`<p class="osmuk-body">You can also enter your 24-hour administrator code here.</p>`))
			}
			content = bytes.ReplaceAll(content, []byte("<!-- OTP_LOCAL_NOTICE -->"), []byte(`<p class="osmuk-body">Local test mode. Get the code from the <a href="/admin/outbox">admin email outbox</a>. No email was sent.</p>`))
		}
		content = bytes.ReplaceAll(content, []byte("OTP_DESTINATION"), []byte(template.HTMLEscapeString(codeDestination)))
		content = bytes.ReplaceAll(content, []byte("OTP_CHANNEL"), []byte(codeChannel))
		if demoError != "" {
			content = addDemoCodeError(content, demoError)
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Keep the original demo page styling and scripts available. The global
	// server policy is intentionally stricter for API responses, but these
	// pages already depend on the bundled GOV.UK assets and their existing JS.
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; form-action 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'")
	w.WriteHeader(responseStatus)
	if r.Method != http.MethodHead {
		_, _ = w.Write(content)
	}
}

func addDemoCodeError(content []byte, message string) []byte {
	errorHTML := `<div class="osmuk-error-summary" role="alert" tabindex="-1" style="border:5px solid #d4351c;padding:20px;margin:0 0 30px;color:#d4351c"><h2 class="osmuk-heading-m" style="margin:0 0 10px">There is a problem</h2><p style="margin:0"><a href="#verificationCode" style="color:#d4351c">` + template.HTMLEscapeString(message) + `</a></p></div>`
	marker := `<div id="kc-content-wrapper" class="">`
	return []byte(strings.Replace(string(content), marker, marker+errorHTML, 1))
}

const skilledWorkerRoute = "Skilled Worker Route"

var skilledWorkerActivities = []string{
	"Work (subject to the restrictions below).",
	"Study, subject to Academic Technology Approval Scheme (ATAS) conditions.",
	"Use the National Health Service (NHS) in a similar way to permanent UK residents.",
	"Rent somewhere to live.",
	"Access a current account with a bank or building society in the UK.",
}

func isSkilledWorkerStatus(status string) bool {
	return strings.EqualFold(strings.TrimSpace(status), skilledWorkerRoute)
}

var demoEntriesRow = regexp.MustCompile(`(?s)<div class="(?:osmuk|govuk)-summary-list__row">\s*<dt[^>]*>Number of entries allowed</dt>\s*<dd[^>]*>.*?</dd>\s*</div>`)
var demoActivitiesList = regexp.MustCompile(`(?s)(What you can do in the UK</h2>\s*<p[^>]*>).*?(</p>\s*<ul[^>]*>).*?(</ul>)`)

func populateDemo(content []byte, p Person) []byte {
	name := template.HTMLEscapeString(p.FullName())
	content = []byte(strings.ReplaceAll(string(content), "Example Person", name))
	content = replaceDemoSummary(content, "Nationality", p.Nationality)
	content = replaceDemoSummary(content, "Status", p.RecordedStatus)
	content = replaceDemoSummary(content, "Accompanied by", p.AccompaniedBy)
	content = replaceDemoSummary(content, "Valid from", formatDemoDate(p.ValidFrom))
	content = replaceDemoSummary(content, "Valid until", formatDemoDate(p.ValidUntil))
	if isSkilledWorkerStatus(p.RecordedStatus) {
		content = demoEntriesRow.ReplaceAll(content, nil)
		var items strings.Builder
		for _, activity := range skilledWorkerActivities {
			items.WriteString("<li>" + template.HTMLEscapeString(activity) + "</li>")
		}
		content = demoActivitiesList.ReplaceAll(content, []byte("${1}Illustrative activities for this fictional record.${2}"+items.String()+"${3}"))
	} else {
		content = replaceDemoSummary(content, "Number of entries allowed", p.EntriesAllowed)
	}
	if p.ShareCode != "" {
		content = []byte(strings.ReplaceAll(string(content), "DEMO-NOT-VALID", template.HTMLEscapeString(p.ShareCode)))
	}
	if p.DateOfBirth != "" {
		content = replaceDemoSummary(content, "Date of birth", formatDemoDate(p.DateOfBirth))
	}
	if p.HasPhoto {
		content = []byte(strings.ReplaceAll(string(content), "/assets/images/demo-portrait.svg", "/gov/photo"))
	}
	return content
}

func wireDemoPDFLinks(content []byte, pdfPath string) []byte {
	s := string(content)
	s = regexp.MustCompile(`(?s)(<a[^>]*aria-label="Print this page"[^>]*href=")[^"]*(")`).ReplaceAllString(s, `${1}#print-page${2}`)
	s = strings.Replace(s, `aria-label="Print this page"`, `aria-label="Print this page" onclick="window.print(); return false;"`, 1)
	s = regexp.MustCompile(`(?s)(<a[^>]*aria-label="Print this page"[^>]*) data-local-unavailable="link"`).ReplaceAllString(s, `${1}`)
	s = regexp.MustCompile(`(?s)(<a[^>]*aria-label="Download PDF"[^>]*href=")[^"]*(")`).ReplaceAllString(s, `${1}`+pdfPath+`${2}`)
	s = regexp.MustCompile(`(?s)(<a[^>]*aria-label="Download PDF"[^>]*) data-local-unavailable="link"`).ReplaceAllString(s, `${1}`)
	if !strings.Contains(s, "aria-label=\"Download PDF\"") {
		marker := "</main>"
		s = strings.Replace(s, marker, `<p class="osmuk-body"><a class="osmuk-link" aria-label="Print this page" href="#print-page" onclick="window.print(); return false;">Print this page</a> &nbsp; <a class="osmuk-link" aria-label="Download PDF" href="`+pdfPath+`">Download PDF</a></p>`+marker, 1)
	}
	return []byte(s)
}

func formatDemoDate(value string) string {
	d, err := time.Parse("2006-01-02", value)
	if err != nil {
		return ""
	}
	return d.Format("02 January 2006")
}

func replaceDemoSummary(content []byte, label, value string) []byte {
	if value == "" {
		return content
	}
	pattern := regexp.MustCompile(`(?s)(<dt[^>]*>\s*` + regexp.QuoteMeta(label) + `\s*</dt>\s*<dd[^>]*>).*?(</dd>)`)
	return pattern.ReplaceAll(content, []byte(`${1}`+template.HTMLEscapeString(value)+`${2}`))
}

// Render only the submitted values, never details from another stored record.
func renderDemoIdentityMismatch(w http.ResponseWriter, documentNumber, dob string) {
	content, err := web.Files.ReadFile("pages/demo/identity-mismatch.html")
	if err != nil {
		http.Error(w, "Unable to display the error page", http.StatusInternalServerError)
		return
	}
	page, err := template.New("identity-mismatch").Parse(string(content))
	var body bytes.Buffer
	if err == nil {
		displayDOB := formatDemoDate(dob)
		if displayDOB == "" {
			displayDOB = dob
		}
		err = page.Execute(&body, struct{ DocumentNumber, DateOfBirth string }{documentNumber, displayDOB})
	}
	if err != nil {
		http.Error(w, "Unable to display the error page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; form-action 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_, _ = w.Write(body.Bytes())
}
