package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeFilename(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want string
	}{
		{in: " hello.txt ", want: "hello.txt"},
		{in: "C:\\fakepath\\photo.png", want: "photo.png"},
		{in: "../notes.md", want: "notes.md"},
		{in: ".", want: ""},
		{in: "..", want: ""},
		{in: "a<b>.txt", want: "a_b_.txt"},
		{in: "a\x01b.txt", want: "ab.txt"},
	}

	for _, tc := range cases {
		got := sanitizeFilename(tc.in)
		if got != tc.want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCreateUniqueFileAutoRename(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("existing"), 0o644); err != nil {
		t.Fatalf("failed to create fixture: %v", err)
	}

	name, file, err := createUniqueFile(dir, "file.txt")
	if err != nil {
		t.Fatalf("createUniqueFile returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = file.Close()
	})

	if name != "file (1).txt" {
		t.Fatalf("created name %q, want %q", name, "file (1).txt")
	}

	_ = file.Close()
	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		t.Fatalf("expected file to exist: %v", err)
	}
}
