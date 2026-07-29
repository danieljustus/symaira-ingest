package filetype

import "testing"

func TestLabelToMIME(t *testing.T) {
	tests := []struct {
		label    string
		expected string
	}{
		{"pdf", "application/pdf"},
		{"png", "image/png"},
		{"jpeg", "image/jpeg"},
		{"tiff", "image/tiff"},
		{"webp", "image/webp"},
		{"txt", "text/plain"},
		{"text", "text/plain"},
		{"csv", "text/csv"},
		{"markdown", "text/markdown"},
		{"html", "text/html"},
		{"rtf", "application/rtf"},
		{"docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{"xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
		{"odt", "application/vnd.oasis.opendocument.text"},
		{"eml", "message/rfc822"},
		{"PDF", "application/pdf"},
		{"TXT", "text/plain"},
		{"Markdown", "text/markdown"},
		// Unmapped labels
		{"zip", ""},
		{"exe", ""},
		{"unknown", ""},
		{"", ""},
	}

	for _, tc := range tests {
		t.Run(tc.label, func(t *testing.T) {
			got := LabelToMIME(tc.label)
			if got != tc.expected {
				t.Errorf("LabelToMIME(%q) = %q, want %q", tc.label, got, tc.expected)
			}
		})
	}
}

func TestAvailable(t *testing.T) {
	// Available should always return a value without error
	_ = Available()
}

func TestDetectWithoutMagika(t *testing.T) {
	// When magika is not installed, Detect should return nil, nil
	pred, err := Detect("/nonexistent/file.pdf", nil)
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}
	if pred != nil {
		t.Fatalf("Detect returned non-nil prediction when magika is unavailable")
	}
}

func TestVerifyAgainstGuessWithoutMagika(t *testing.T) {
	// Should not panic or error when magika is not installed
	logged := false
	logf := func(format string, args ...any) {
		logged = true
	}
	VerifyAgainstGuess("/nonexistent/file.pdf", "application/pdf", logf)
	if logged {
		t.Fatal("unexpected log when magika is unavailable")
	}
}

func TestVerifyAgainstGuessEmptyPath(t *testing.T) {
	// Should not panic on empty paths
	VerifyAgainstGuess("", "application/pdf", nil)
}
