package clock

import (
	"strconv"
	"strings"
)

var monthNames = map[string]int{
	"january": 1, "jan": 1,
	"february": 2, "feb": 2,
	"march": 3, "mar": 3,
	"april": 4, "apr": 4,
	"may":  5,
	"june": 6, "jun": 6,
	"july": 7, "jul": 7,
	"august": 8, "aug": 8,
	"september": 9, "sep": 9, "sept": 9,
	"october": 10, "oct": 10,
	"november": 11, "nov": 11,
	"december": 12, "dec": 12,
}

var dowNames = map[string]int{
	"sunday": 0, "sun": 0,
	"monday": 1, "mon": 1,
	"tuesday": 2, "tue": 2, "tues": 2,
	"wednesday": 3, "wed": 3,
	"thursday": 4, "thu": 4, "thur": 4, "thurs": 4,
	"friday": 5, "fri": 5,
	"saturday": 6, "sat": 6,
}

func parseField(spec string, lo, hi int, names map[string]int, norm func(int) int) (uint64, error) {
	if norm == nil {
		norm = func(v int) int { return v }
	}
	bits := uint64(0)
	for _, part := range strings.Split(spec, ",") {
		start, end, step, err := splitRange(strings.TrimSpace(part), lo, hi, names)
		if err != nil {
			return 0, err
		}
		for v := start; v <= end; v += step {
			nv := norm(v)
			if nv < lo || nv > hi {
				return 0, ErrInvalidSchedule
			}
			bits |= 1 << uint(nv)
		}
	}
	if bits == 0 {
		return 0, ErrInvalidSchedule
	}
	return bits, nil
}

func atoi(s string, names map[string]int) (int, error) {
	if i, ok := names[strings.ToLower(s)]; ok {
		return i, nil
	}
	return strconv.Atoi(s)
}

func splitRange(s string, lo, hi int, names map[string]int) (start, end, step int, err error) {
	step = 1
	body := s
	if i := strings.IndexByte(s, '/'); i >= 0 {
		body = s[:i]
		step, err = strconv.Atoi(strings.TrimSpace(s[i+1:]))
		if err != nil || step < 1 {
			return 0, 0, 0, ErrInvalidSchedule
		}
	}
	body = strings.TrimSpace(body)
	switch {
	case body == "*":
		return lo, hi, step, nil
	case strings.Contains(body, "-"):
		parts := strings.SplitN(body, "-", 2)
		a, e1 := atoi(strings.TrimSpace(parts[0]), names)
		b, e2 := atoi(strings.TrimSpace(parts[1]), names)
		if e1 != nil || e2 != nil {
			return 0, 0, 0, ErrInvalidSchedule
		}
		return a, b, step, nil
	default:
		v, e := atoi(body, names)
		if e != nil {
			return 0, 0, 0, ErrInvalidSchedule
		}
		if step > 1 {
			return v, hi, step, nil
		}
		return v, v, step, nil
	}
}
