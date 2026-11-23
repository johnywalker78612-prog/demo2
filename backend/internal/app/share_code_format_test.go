package app

import "testing"

func TestShareCodeFormat(t *testing.T) {
	person := Person{FirstName: "Example", ShareCode: "legacy"}
	if err := apply(&person, map[string]string{"share_code": "legacy"}); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"ghy76h67a", "ghy-76h-67a"} {
		if err := apply(&person, map[string]string{"share_code": input}); err != nil {
			t.Fatal(err)
		}
		if person.ShareCode != "GHY-76H-67A" {
			t.Fatalf("unexpected format %q", person.ShareCode)
		}
	}
	if err := apply(&person, map[string]string{"share_code": "GHY--76H-67A"}); err == nil {
		t.Fatal("expected malformed code to be rejected")
	}
}
