package main

import (
	"strings"
	"testing"
)

func TestParseWatchDirsDropsEmptyEntries(t *testing.T) {
	got := parseWatchDirs(` /data/a,,"/data/b", '' `)
	want := []string{"/data/a", "/data/b"}

	if len(got) != len(want) {
		t.Fatalf("unexpected dirs: got %#v want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected dirs: got %#v want %#v", got, want)
		}
	}
}

func TestRedactURLForLog(t *testing.T) {
	got := redactURLForLog("https://user:secret@example.test/archive?token=abc&prefix=ok")

	if strings.Contains(got, "secret") || strings.Contains(got, "token=abc") {
		t.Fatalf("URL was not redacted: %s", got)
	}
	if !strings.Contains(got, "prefix=ok") {
		t.Fatalf("non-sensitive query parameter should be preserved: %s", got)
	}
}
