package i18n

import "testing"

func TestDefaultIsEnglish(t *testing.T) {
	if Current() != EN {
		t.Fatalf("package default Current() = %q, want %q", Current(), EN)
	}
}

func TestResolve(t *testing.T) {
	tests := []struct {
		in      string
		want    Lang
		wantErr bool
	}{
		{"", EN, false},
		{"en", EN, false},
		{"English", EN, false},
		{"английский", EN, false},
		{"ru", RU, false},
		{"RU", RU, false},
		{" russian ", RU, false},
		{"русский", RU, false},
		{"fr", EN, true},
		{"de", EN, true},
	}
	for _, tt := range tests {
		got, err := Resolve(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("Resolve(%q) err = %v, wantErr %v", tt.in, err, tt.wantErr)
		}
		if got != tt.want {
			t.Errorf("Resolve(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTAndSet(t *testing.T) {
	t.Cleanup(func() { Set(EN) }) // restore the package default for other tests

	Set(RU)
	if Current() != RU {
		t.Fatalf("Current() = %q, want %q", Current(), RU)
	}
	if got := T("привет", "hello"); got != "привет" {
		t.Errorf("T under RU = %q, want %q", got, "привет")
	}

	Set(EN)
	if Current() != EN {
		t.Fatalf("Current() = %q, want %q", Current(), EN)
	}
	if got := T("привет", "hello"); got != "hello" {
		t.Errorf("T under EN = %q, want %q", got, "hello")
	}

	// Any unknown code falls back to EN.
	Set(RU)
	Set(Lang("xx"))
	if Current() != EN {
		t.Errorf("Set(unknown) Current() = %q, want %q", Current(), EN)
	}
}

func TestNamesAndDisplay(t *testing.T) {
	names := Names()
	if len(names) != 2 || names[0] != EN || names[1] != RU {
		t.Fatalf("Names() = %v, want [en ru]", names)
	}
	if RU.Display() != "Русский" || EN.Display() != "English" {
		t.Errorf("Display() = %q/%q", RU.Display(), EN.Display())
	}
}
