package app

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

var reportLocation = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		panic(err)
	}
	return loc
}()

type PeopleList struct {
	Items                                                []Person
	Total, Page, PageSize, Pages, Start, End             int
	Query, Field, Sort, Order, Nationality, Verification string
	Nationalities                                        []string
	Previous, Next                                       string
}

func phoneDigits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}
func choice(value, fallback string, allowed ...string) string {
	for _, a := range allowed {
		if value == a {
			return value
		}
	}
	return fallback
}
func queryPeople(d database, q url.Values) PeopleList {
	l := PeopleList{Items: []Person{}, Query: strings.TrimSpace(q.Get("q")), Field: choice(q.Get("field"), "all", "all", "name", "email", "passport", "phone"), Sort: choice(q.Get("sort"), "created_at", "created_at", "updated_at", "name", "email", "nationality", "phone_number", "passport_number"), Order: choice(q.Get("order"), "desc", "asc", "desc"), Nationality: q.Get("nationality"), Verification: choice(q.Get("verification"), "all", "all", "verified", "pending"), PageSize: 25}
	l.Page, _ = strconv.Atoi(q.Get("page"))
	if l.Page < 1 {
		l.Page = 1
	}
	needle := strings.ToLower(l.Query)
	nationalities := map[string]bool{}
	for _, p := range d.People {
		if p.Nationality != "" {
			nationalities[p.Nationality] = true
		}
		if l.Nationality != "" && p.Nationality != l.Nationality {
			continue
		}
		if l.Verification == "verified" && p.EmailVerifiedAt == nil || l.Verification == "pending" && p.EmailVerifiedAt != nil {
			continue
		}
		match := needle == ""
		checks := map[string]string{"name": p.FullName(), "email": p.Email, "passport": p.PassportNumber, "phone": p.PhoneNumber}
		for key, value := range checks {
			if l.Field != "all" && l.Field != key {
				continue
			}
			if strings.Contains(strings.ToLower(value), needle) {
				match = true
			}
			if key == "phone" && len(phoneDigits(needle)) > 0 && strings.Contains(phoneDigits(value), phoneDigits(needle)) && strings.Trim(needle, "+0123456789 ()-.") == "" {
				match = true
			}
		}
		if match {
			l.Items = append(l.Items, p)
		}
	}
	for n := range nationalities {
		l.Nationalities = append(l.Nationalities, n)
	}
	sort.Strings(l.Nationalities)
	value := func(p Person) string {
		switch l.Sort {
		case "name":
			return strings.ToLower(p.FullName())
		case "email":
			return strings.ToLower(p.Email)
		case "nationality":
			return strings.ToLower(p.Nationality)
		case "passport_number":
			return strings.ToLower(p.PassportNumber)
		case "phone_number":
			return phoneDigits(p.PhoneNumber)
		case "updated_at":
			return p.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000000000Z")
		default:
			return p.CreatedAt.UTC().Format("2006-01-02T15:04:05.000000000Z")
		}
	}
	sort.Slice(l.Items, func(i, j int) bool {
		a, b := value(l.Items[i]), value(l.Items[j])
		if a == b {
			a, b = l.Items[i].ID, l.Items[j].ID
		}
		if l.Order == "asc" {
			return a < b
		}
		return a > b
	})
	l.Total = len(l.Items)
	l.Pages = (l.Total + l.PageSize - 1) / l.PageSize
	if l.Pages < 1 {
		l.Pages = 1
	}
	if l.Page > l.Pages {
		l.Page = l.Pages
	}
	start := (l.Page - 1) * l.PageSize
	l.End = min(start+l.PageSize, l.Total)
	l.Items = l.Items[start:l.End]
	if l.Total > 0 {
		l.Start = start + 1
	}
	link := func(page int) string {
		v := url.Values{}
		for k, vals := range q {
			v[k] = append([]string(nil), vals...)
		}
		v.Set("page", strconv.Itoa(page))
		return "/admin/people?" + v.Encode()
	}
	if l.Page > 1 {
		l.Previous = link(l.Page - 1)
	}
	if l.Page < l.Pages {
		l.Next = link(l.Page + 1)
	}
	return l
}

type MonthReport struct {
	Label   string `json:"label"`
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
	Height  int    `json:"-"`
}
type Report struct {
	CurrentTotal    int           `json:"current_total"`
	ThisMonth       int           `json:"this_month"`
	ThreeMonths     int           `json:"three_months"`
	SixMonths       int           `json:"six_months"`
	TwelveMonths    int           `json:"twelve_months"`
	SelectedMonths  int           `json:"selected_months"`
	SelectedAdded   int           `json:"selected_added"`
	SelectedDeleted int           `json:"selected_deleted"`
	Monthly         []MonthReport `json:"monthly"`
	From            string        `json:"from"`
	Through         string        `json:"through"`
	Timezone        string        `json:"timezone"`
}

func reportMonths(v url.Values) int {
	switch v.Get("months") {
	case "1":
		return 1
	case "3":
		return 3
	case "6":
		return 6
	default:
		return 12
	}
}

// Creation audit events survive deletion. Deduplicate by record ID and fall back
// to CreatedAt for older/imported records with no creation event. Updates are not additions.
func makeReport(d database, now time.Time, months int) Report {
	if months != 1 && months != 3 && months != 6 && months != 12 {
		months = 12
	}
	now = now.In(reportLocation)
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, reportLocation)
	r := Report{CurrentTotal: len(d.People), SelectedMonths: months, Timezone: "Asia/Kolkata", From: start.AddDate(0, 1-months, 0).Format("02 Jan 2006"), Through: now.Format("02 Jan 2006"), Monthly: []MonthReport{}}
	created := map[string]time.Time{}
	for _, e := range d.Audit {
		if e.Action == "person.created" {
			if old, ok := created[e.ResourceID]; !ok || e.At.Before(old) {
				created[e.ResourceID] = e.At
			}
		}
	}
	for id, p := range d.People {
		if _, ok := created[id]; !ok {
			created[id] = p.CreatedAt
		}
	}
	buckets := make([]MonthReport, 12)
	for i := range buckets {
		buckets[i].Label = start.AddDate(0, i-11, 0).Format("Jan 2006")
	}
	index := func(at time.Time) int {
		if at.IsZero() || at.After(now) {
			return -1
		}
		at = at.In(reportLocation)
		diff := (now.Year()-at.Year())*12 + int(now.Month()-at.Month())
		if diff < 0 || diff > 11 {
			return -1
		}
		return 11 - diff
	}
	for _, at := range created {
		if i := index(at); i >= 0 {
			buckets[i].Added++
		}
	}
	for _, e := range d.Audit {
		if e.Action == "person.deleted" {
			if i := index(e.At); i >= 0 {
				buckets[i].Deleted++
			}
		}
	}
	for i, b := range buckets {
		r.TwelveMonths += b.Added
		if i >= 6 {
			r.SixMonths += b.Added
		}
		if i >= 9 {
			r.ThreeMonths += b.Added
		}
		if i == 11 {
			r.ThisMonth = b.Added
		}
	}
	r.Monthly = append(r.Monthly, buckets[12-months:]...)
	maximum := 1
	for _, b := range r.Monthly {
		r.SelectedAdded += b.Added
		r.SelectedDeleted += b.Deleted
		if b.Added > maximum {
			maximum = b.Added
		}
	}
	for i := range r.Monthly {
		r.Monthly[i].Height = r.Monthly[i].Added * 100 / maximum
	}
	return r
}
