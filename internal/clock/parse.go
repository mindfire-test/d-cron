package clock

import (
	"strconv"
	"strings"
)

// parseField builds a bitset (bit v set for each accepted value in [lo, hi])
// from a single cron field. norm is applied to each value before the range
// check, enabling the 7->0 day-of-week alias.
func parseField(spec string, lo, hi int, norm func(int) int) (uint64, error) {
	if norm == nil {
		norm = func(v int) int { return v }
	}
	bits := uint64(0)
	for _, part := range strings.Split(spec, ",") {
		start, end, step, err := splitRange(strings.TrimSpace(part), lo, hi)
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

// splitRange interprets "*", "*/n", "a-b", "a-b/n", "a", and "a/n" where "a/n"
// means "from a to the field maximum, stepping by n".
func splitRange(s string, lo, hi int) (start, end, step int, err error) {
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
		a, e1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		b, e2 := strconv.Atoi(strings.TrimSpace(parts[1]))
		if e1 != nil || e2 != nil {
			return 0, 0, 0, ErrInvalidSchedule
		}
		return a, b, step, nil
	default:
		v, e := strconv.Atoi(body)
		if e != nil {
			return 0, 0, 0, ErrInvalidSchedule
		}
		if step > 1 {
			return v, hi, step, nil
		}
		return v, v, step, nil
	}
}
