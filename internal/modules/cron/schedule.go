package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// schedule is a parsed 5-field cron expression (minute hour dom month dow).
type schedule struct {
	min, hour, dom, month, dow uint64
}

func parseSchedule(expr string) (*schedule, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron expression must have 5 fields, got %d", len(fields))
	}
	var s schedule
	var err error
	if s.min, err = parseField(fields[0], 0, 59); err != nil {
		return nil, err
	}
	if s.hour, err = parseField(fields[1], 0, 23); err != nil {
		return nil, err
	}
	if s.dom, err = parseField(fields[2], 1, 31); err != nil {
		return nil, err
	}
	if s.month, err = parseField(fields[3], 1, 12); err != nil {
		return nil, err
	}
	if s.dow, err = parseField(fields[4], 0, 6); err != nil {
		return nil, err
	}
	return &s, nil
}

func parseField(f string, min, max int) (uint64, error) {
	var bits uint64
	for _, part := range strings.Split(f, ",") {
		step := 1
		rng := part
		if i := strings.Index(part, "/"); i >= 0 {
			var err error
			step, err = strconv.Atoi(part[i+1:])
			if err != nil || step <= 0 {
				return 0, fmt.Errorf("bad step in %q", part)
			}
			rng = part[:i]
		}
		lo, hi := min, max
		if rng != "*" {
			if i := strings.Index(rng, "-"); i >= 0 {
				var err error
				if lo, err = strconv.Atoi(rng[:i]); err != nil {
					return 0, err
				}
				if hi, err = strconv.Atoi(rng[i+1:]); err != nil {
					return 0, err
				}
			} else {
				v, err := strconv.Atoi(rng)
				if err != nil {
					return 0, fmt.Errorf("bad value %q", rng)
				}
				lo, hi = v, v
			}
		}
		if lo < min || hi > max || lo > hi {
			return 0, fmt.Errorf("field %q out of range [%d,%d]", part, min, max)
		}
		for v := lo; v <= hi; v += step {
			bits |= 1 << uint(v)
		}
	}
	return bits, nil
}

func (s *schedule) matches(t time.Time) bool {
	dow := uint64(1) << uint(int(t.Weekday()))
	domMatch := s.dom&(1<<uint(t.Day())) != 0
	dowMatch := s.dow&dow != 0
	dayOK := domMatch && dowMatch
	// standard cron: if either dom or dow is restricted (not "*"), OR them
	if s.dom != allBits(1, 31) || s.dow != allBits(0, 6) {
		dayOK = domMatch || dowMatch
		if s.dom == allBits(1, 31) {
			dayOK = dowMatch
		} else if s.dow == allBits(0, 6) {
			dayOK = domMatch
		}
	}
	return s.min&(1<<uint(t.Minute())) != 0 &&
		s.hour&(1<<uint(t.Hour())) != 0 &&
		s.month&(1<<uint(int(t.Month()))) != 0 &&
		dayOK
}

func allBits(lo, hi int) uint64 {
	var b uint64
	for v := lo; v <= hi; v++ {
		b |= 1 << uint(v)
	}
	return b
}
