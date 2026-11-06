package dcron

import "testing"

func TestSanitizeLogValue(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"clean-job", "clean-job"},
		{"job\nwith\nnewlines", "jobwithnewlines"},
		{"job\r\nwith\r\ncrlf", "jobwithcrlf"},
		{"injection\n[ERROR] Fake log entry\r", "injection[ERROR] Fake log entry"},
	}

	for _, tc := range cases {
		got := sanitizeLogValue(tc.input)
		if got != tc.expected {
			t.Errorf("sanitizeLogValue(%q) = %q; want %q", tc.input, got, tc.expected)
		}
	}
}
