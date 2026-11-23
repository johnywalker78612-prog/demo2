package app

import "testing"

func TestEntriesAllowed(t *testing.T) {
	for _, value := range []string{"", "0", "1", "2", "3", "44", "777", "Multiple", "multiple", " MULTIPLE "} {
		t.Run("accept_"+value, func(t *testing.T) {
			p := Person{FirstName: "Example"}
			if err := apply(&p, map[string]string{"entries_allowed": value}); err != nil {
				t.Fatal(err)
			}
			if (value == "multiple" || value == " MULTIPLE ") && p.EntriesAllowed != "Multiple" {
				t.Fatal("Multiple not normalized")
			}
		})
	}
	for _, value := range []string{"-1", "+1", "1.5", "1e3", "1,000", "1 2", "Unlimited", "Single"} {
		t.Run("reject_"+value, func(t *testing.T) {
			p := Person{FirstName: "Example"}
			if err := apply(&p, map[string]string{"entries_allowed": value}); err == nil {
				t.Fatal("invalid entries accepted")
			}
		})
	}
}
