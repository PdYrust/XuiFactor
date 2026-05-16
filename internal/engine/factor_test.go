package engine

import "testing"

func TestParseFactor(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"1", 1_000_000},
		{"1.1", 1_100_000},
		{"1.2", 1_200_000},
		{"1.234567", 1_234_567},
		{"5", 5_000_000},
		{"5000", 5_000_000_000},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseFactor(tt.input)
			if err != nil {
				t.Fatalf("ParseFactor() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("ParseFactor() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestParseFactorRejectsInvalidInput(t *testing.T) {
	for _, input := range []string{"", "0.9", "0.999999", "1.", ".1", "1.0000000", "abc", "-1", "1.2.3"} {
		t.Run(input, func(t *testing.T) {
			if _, err := ParseFactor(input); err == nil {
				t.Fatalf("expected error for %q", input)
			}
		})
	}
}
