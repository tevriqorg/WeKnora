package handler

import "testing"

func TestAIBOXExternalResourcePathIsConfined(t *testing.T) {
	prefix := "local://7/aibox-uat-soak/"
	cases := []struct {
		path string
		want bool
	}{
		{"local://7/aibox-uat-soak/reports/1.pdf", true},
		{"local://7/aibox-uat-soak/reports/1.mp4", true},
		{"local://7/aibox-uat-soak/", false},
		{"local://7/aibox-uat-soak/.", false}, // root disguised as a relative file
		{"local://7/aibox-uat-soak/../other", false},
		{"local://7/aibox-uat-soak/reports//1.pdf", false},
		{"local://7/aibox-uat-soak/%2e%2e/x", false},
		{"local://7/aibox-uat-soak/reports\\secret", false},
		{"local://7/private/test", false},
		{"file:///etc/passwd", false},
	}
	for _, tc := range cases {
		if got := safeAIBOXSourceResourcePath(prefix, tc.path); got != tc.want {
			t.Errorf("path %q: got %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestAIBOXExternalSourceFingerprintMustBeSHA256Hex(t *testing.T) {
	cases := []struct {
		hash string
		want bool
	}{
		{"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", true},
		{"ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789", true},
		{"zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", false},
		{"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := validAIBOXSourceSHA256(tc.hash); got != tc.want {
			t.Errorf("hash length %d valid=%v, want %v", len(tc.hash), got, tc.want)
		}
	}
}
