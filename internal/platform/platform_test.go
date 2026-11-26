package platform

import "testing"

func TestParseAndFormatHotkey(t *testing.T) {
	cases := []struct {
		in      string
		goos    string
		want    string
		wantErr bool
	}{
		{"Option+V", "darwin", "Option+V", false},
		{"alt+v", "windows", "Alt+V", false},
		{"ctrl+shift+space", "darwin", "Ctrl+Shift+Space", false},
		{"cmd+f12", "darwin", "Cmd+F12", false},
		{"V", "darwin", "", true},
		{"Option+F13", "darwin", "", true},
		{"Unknown+V", "darwin", "", true},
	}
	for _, tc := range cases {
		hk, err := ParseHotkey(tc.in)
		if (err != nil) != tc.wantErr {
			t.Fatalf("ParseHotkey(%q) err=%v wantErr=%v", tc.in, err, tc.wantErr)
		}
		if err == nil {
			got := FormatHotkey(hk, tc.goos)
			if got != tc.want {
				t.Fatalf("FormatHotkey(%q, %q) = %q, want %q", tc.in, tc.goos, got, tc.want)
			}
		}
	}
}

func TestShortcutKeysReserveTForTemplates(t *testing.T) {
	if got := HistoryShortcutKey(0); got != "1" {
		t.Fatalf("idx 0 = %q", got)
	}
	if got := HistoryShortcutKey(9); got != "0" {
		t.Fatalf("idx 9 = %q", got)
	}
	for i := 0; i < 40; i++ {
		if got := HistoryShortcutKey(i); got == "T" {
			t.Fatalf("HistoryShortcutKey(%d) returned 'T', which must be reserved for Templates", i)
		}
	}
	if got := HistoryShortcutKey(28); got != "S" {
		t.Fatalf("idx 28 = %q, want S", got)
	}
	if got := HistoryShortcutKey(29); got != "U" {
		t.Fatalf("idx 29 = %q, want U (skipping T)", got)
	}

	if got := SubmenuShortcutKey(0); got != "1" {
		t.Fatalf("sub idx 0 = %q", got)
	}
	if got := SubmenuShortcutKey(9); got != "0" {
		t.Fatalf("sub idx 9 = %q", got)
	}
	if got := SubmenuShortcutKey(10); got != "A" {
		t.Fatalf("sub idx 10 = %q", got)
	}
}
