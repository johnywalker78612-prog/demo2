package app

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
)

func privateRecordPDF(data map[string]any) ([]byte, error) {
	p := fpdf.New("P", "mm", "A4", "")
	p.AddUTF8FontFromBytes("Private", "", goregular.TTF)
	p.AddUTF8FontFromBytes("Private", "B", gobold.TTF)
	p.SetMargins(20, 20, 20)
	p.SetAutoPageBreak(true, 30)
	p.SetTitle("Private Records - unverified shared record", true)
	p.SetAuthor("Private Records", true)
	p.SetHeaderFunc(func() {
		p.SetFillColor(22, 77, 70)
		p.Rect(0, 0, 210, 32, "F")
		p.SetTextColor(255, 255, 255)
		p.SetFont("Private", "B", 21)
		p.SetXY(20, 10)
		p.Cell(170, 12, "Private Records")
		p.SetY(42)
		p.SetTextColor(30, 47, 45)
		p.SetFont("Private", "B", 11)
		p.MultiCell(170, 6, "", "", "L", false)
		p.Ln(6)
	})
	p.SetFooterFunc(func() {
		p.SetY(-23)
		p.SetFont("Private", "", 8)
		p.SetTextColor(70, 85, 82)
		p.MultiCell(170, 4, "Independent private service. Not a government document.\nA downloaded copy may be outdated; check the active sharing link.", "", "L", false)
		p.CellFormat(170, 4, fmt.Sprintf("Page %d", p.PageNo()), "", 0, "R", false, 0, "")
	})
	p.AddPage()
	p.SetFont("Private", "B", 18)
	p.CellFormat(170, 10, "Shared record summary", "", 1, "L", false, 0, "")
	p.SetFont("Private", "", 10)
	p.MultiCell(170, 5, "The following information was entered into a private records system. It has not been verified against official sources.", "", "L", false)
	p.Ln(7)
	rows := [][2]string{{"Name", strings.TrimSpace(fmt.Sprint(data["first_name"]) + " " + fmt.Sprint(data["last_name"]))}, {"Nationality", fmt.Sprint(data["nationality"])}, {"Recorded status (unverified)", fmt.Sprint(data["recorded_status"])}, {"Recorded valid from", fmt.Sprint(data["valid_from"])}, {"Recorded valid until", fmt.Sprint(data["valid_until"])}}
	if ref, ok := data["internal_reference"].(string); ok && ref != "" {
		rows = append(rows, [2]string{"Internal sharing reference", ref})
	}
	if link, ok := data["private_link"].(string); ok && link != "" {
		rows = append(rows, [2]string{"Private Records access link", link})
	}
	if expiry, ok := data["expires_at"].(time.Time); ok {
		rows = append(rows, [2]string{"Sharing access expires", expiry.UTC().Format("02 Jan 2006, 15:04 UTC")})
	}
	for _, row := range rows {
		value := row[1]
		if value == "" || value == "<nil>" {
			value = "Not provided"
		}
		p.SetFont("Private", "", 11)
		needed := float64(len(p.SplitText(value, 170)))*6 + 15
		if p.GetY()+needed > 267 {
			p.AddPage()
		}
		p.SetFont("Private", "B", 10)
		p.MultiCell(170, 5, row[0], "", "L", false)
		p.SetFont("Private", "", 11)
		p.MultiCell(170, 6, value, "", "L", false)
		p.Ln(3)
		p.SetDrawColor(211, 221, 217)
		p.Line(20, p.GetY(), 190, p.GetY())
		p.Ln(6)
	}
	p.SetFont("Private", "", 9)
	p.MultiCell(170, 5, "Exported: "+time.Now().UTC().Format("02 Jan 2006, 15:04 UTC")+"\nPassport numbers, date of birth, contact details and photos are excluded from this shared export.", "", "L", false)
	var b bytes.Buffer
	err := p.Output(&b)
	return b.Bytes(), err
}
func (s *Server) sharedPDF(w http.ResponseWriter, r *http.Request) {
	data, ok := s.shared(r.PathValue("token"))
	if !ok {
		fail(w, 404, "Link is invalid, expired, or revoked")
		return
	}
	data["internal_reference"] = r.PathValue("token")
	data["private_link"] = s.mailURL("/shared/" + r.PathValue("token"))
	pdf, err := privateRecordPDF(data)
	if err != nil {
		fail(w, 500, "Could not create private-record PDF")
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="private-record.pdf"`)
	w.WriteHeader(200)
	_, _ = w.Write(pdf)
}

func demoPDFValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "Not provided"
	}
	return value
}

func demoPDFDate(value string) string {
	if d, err := time.Parse("2006-01-02", value); err == nil {
		return d.Format("02 January 2006")
	}
	return demoPDFValue(value)
}

func demoPDFHeader(p *fpdf.Fpdf, title string) {
	p.SetFillColor(37, 54, 66)
	p.Rect(0, 0, 210, 28, "F")
	p.SetTextColor(255, 255, 255)
	p.SetFont("Private", "B", 22)
	p.SetXY(18, 8)
	p.Cell(170, 10, "Private Records Demo")
	p.SetTextColor(30, 30, 30)
	p.SetFont("Private", "B", 18)
	p.SetXY(20, 40)
	p.MultiCell(170, 8, title, "", "L", false)
}

func demoStatusPDF(p Person, photo *Photo) ([]byte, error) {
	doc := fpdf.New("P", "mm", "A4", "")
	doc.AddUTF8FontFromBytes("Private", "", goregular.TTF)
	doc.AddUTF8FontFromBytes("Private", "B", gobold.TTF)
	doc.SetMargins(20, 20, 20)
	doc.SetAutoPageBreak(true, 24)
	doc.SetTitle("View your immigration status", true)
	doc.SetAuthor("Private Records", true)
	doc.SetFooterFunc(func() {
		doc.SetY(-16)
		doc.SetFont("Private", "", 8)
		doc.SetTextColor(90, 90, 90)
		doc.CellFormat(170, 4, "Private Records demo - user supplied and unverified", "", 0, "L", false, 0, "")
		doc.CellFormat(0, 4, fmt.Sprintf("%d", doc.PageNo()), "", 0, "R", false, 0, "")
	})
	doc.AddPage()
	demoPDFHeader(doc, "View and prove your immigration status")
	doc.SetY(64)
	doc.SetFont("Private", "B", 16)
	doc.Cell(170, 8, "Your immigration status (eVisa)")
	doc.Ln(15)
	rows := [][2]string{{"Name", demoPDFValue(p.FullName())}, {"Date of birth", demoPDFDate(p.DateOfBirth)}, {"Nationality", demoPDFValue(p.Nationality)}, {"Status", demoPDFValue(p.RecordedStatus)}, {"Accompanied by", demoPDFValue(p.AccompaniedBy)}, {"Valid from", demoPDFDate(p.ValidFrom)}, {"Valid until", demoPDFDate(p.ValidUntil)}}
	for _, row := range rows {
		doc.SetFont("Private", "B", 10)
		doc.Cell(55, 7, row[0])
		doc.SetFont("Private", "", 11)
		doc.MultiCell(115, 7, row[1], "", "L", false)
		doc.Ln(2)
	}
	if photo != nil && len(photo.Data) > 0 {
		imageType := "JPG"
		if photo.ContentType == "image/png" {
			imageType = "PNG"
		}
		doc.RegisterImageOptionsReader("profile-photo", fpdf.ImageOptions{ImageType: imageType, ReadDpi: false}, bytes.NewReader(photo.Data))
		doc.SetFont("Private", "B", 12)
		doc.Cell(170, 7, "Photo")
		doc.Ln(9)
		doc.ImageOptions("profile-photo", 20, doc.GetY(), 45, 58, false, fpdf.ImageOptions{ImageType: imageType, ReadDpi: false}, 0, "")
		doc.SetY(doc.GetY() + 64)
	}
	doc.Ln(8)
	doc.SetFont("Private", "B", 14)
	doc.Cell(170, 8, "Prove your status")
	doc.Ln(10)
	doc.SetFont("Private", "", 11)
	doc.MultiCell(170, 6, "If you need to prove your immigration status to someone , you can do this online with a share code", "", "L", false)
	doc.AddPage()
	demoPDFHeader(doc, "View and prove your immigration status")
	doc.SetY(64)
	doc.SetFont("Private", "B", 16)
	doc.Cell(170, 8, "What you can do in the UK")
	doc.Ln(14)
	doc.SetFont("Private", "", 11)
	if isSkilledWorkerStatus(p.RecordedStatus) {
		doc.MultiCell(170, 6, "Illustrative activities for this fictional record.", "", "L", false)
		doc.Ln(3)
		for _, activity := range skilledWorkerActivities {
			doc.Cell(5, 6, "•")
			doc.MultiCell(165, 6, activity, "", "L", false)
		}
		doc.Ln(3)
		doc.MultiCell(170, 6, "These details have not been verified against official sources.", "", "L", false)
	} else {
		doc.MultiCell(170, 6, "You can:\n\n- stay in the UK according to the dates recorded above\n- visit the UK for tourism and to see family and friends\n- travel in and out of the UK as permitted by the recorded status\n\nThese details were entered by an administrator and have not been verified against official sources.", "", "L", false)
	}
	doc.AddPage()
	demoPDFHeader(doc, "View and prove your immigration status")
	doc.SetY(64)
	doc.SetFont("Private", "B", 16)
	doc.Cell(170, 8, "What you cannot do")
	doc.Ln(14)
	doc.SetFont("Private", "", 11)
	doc.MultiCell(170, 6, "The information in this document does not grant permission to work, access public funds, or remain in the UK. Check the official GOV.UK service for any legal decision.", "", "L", false)
	doc.AddPage()
	demoPDFHeader(doc, "View and prove your immigration status")
	doc.SetY(64)
	doc.SetFont("Private", "B", 16)
	doc.Cell(170, 8, "Keep your details up to date")
	doc.Ln(14)
	doc.SetFont("Private", "", 11)
	doc.MultiCell(170, 6, "If any information is wrong, contact the administrator who created this private record. This downloaded copy may be out of date. It is not an official immigration document.", "", "L", false)
	var out bytes.Buffer
	err := doc.Output(&out)
	return out.Bytes(), err
}

func demoSharePDF(p Person) ([]byte, error) {
	doc := fpdf.New("P", "mm", "A4", "")
	doc.AddUTF8FontFromBytes("Private", "", goregular.TTF)
	doc.AddUTF8FontFromBytes("Private", "B", gobold.TTF)
	doc.SetMargins(20, 20, 20)
	doc.SetAutoPageBreak(true, 24)
	doc.SetTitle("Share your status with someone", true)
	doc.SetAuthor("Private Records", true)
	doc.SetFooterFunc(func() {
		doc.SetY(-16)
		doc.SetFont("Private", "", 8)
		doc.SetTextColor(90, 90, 90)
		doc.Cell(170, 4, "Private Records demo - user supplied and unverified")
	})
	doc.AddPage()
	demoPDFHeader(doc, "View and prove your immigration status")
	doc.SetY(64)
	doc.SetFont("Private", "B", 16)
	doc.Cell(170, 8, "Details you need to share")
	doc.Ln(16)
	doc.SetFont("Private", "B", 12)
	doc.Cell(170, 8, "Share code")
	doc.Ln(10)
	doc.SetFont("Private", "B", 24)
	doc.Cell(170, 12, demoPDFValue(p.ShareCode))
	doc.Ln(18)
	doc.SetFont("Private", "", 11)
	doc.MultiCell(170, 6, "Give this share code and the person's date of birth to the person you want to share the recorded information with.\n\nDate of birth: "+demoPDFDate(p.DateOfBirth)+"\n\nThis is an administrator-entered code. It is not an official GOV.UK share code and cannot prove immigration status.", "", "L", false)
	var out bytes.Buffer
	err := doc.Output(&out)
	return out.Bytes(), err
}
