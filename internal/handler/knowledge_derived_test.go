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
