package engine

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const FactorScale int64 = 1_000_000

func ParseFactor(input string) (int64, error) {
	value := strings.TrimSpace(input)
	if value == "" {
		return 0, errors.New("factor is required")
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 {
		return 0, fmt.Errorf("invalid factor %q", input)
	}
	if parts[0] == "" || !allDigits(parts[0]) {
		return 0, fmt.Errorf("invalid factor %q", input)
	}
	if len(parts) == 2 {
		if parts[1] == "" || len(parts[1]) > 6 || !allDigits(parts[1]) {
			return 0, fmt.Errorf("invalid factor %q", input)
		}
	}

	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid factor %q", input)
	}
	if whole > math.MaxInt64/FactorScale {
		return 0, fmt.Errorf("factor %q is too large", input)
	}

	fraction := int64(0)
	if len(parts) == 2 {
		padded := parts[1] + strings.Repeat("0", 6-len(parts[1]))
		fraction, err = strconv.ParseInt(padded, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid factor %q", input)
		}
	}

	ppm := whole*FactorScale + fraction
	if ppm < FactorScale {
		return 0, errors.New("factor must be greater than or equal to 1")
	}
	return ppm, nil
}

func FormatFactor(ppm int64) string {
	whole := ppm / FactorScale
	fraction := ppm % FactorScale
	if fraction == 0 {
		return strconv.FormatInt(whole, 10)
	}
	text := fmt.Sprintf("%d.%06d", whole, fraction)
	return strings.TrimRight(text, "0")
}

func allDigits(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
