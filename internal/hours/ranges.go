package hours

import (
	"strconv"
	"strings"
)

// NormalizeRange canonicalizes a single "HH:MM-HH:MM" range and reports
// whether it is usable.
//
// It exists because the form is a text box and people paste into it. Times
// copied out of a document arrive with an en- or em-dash instead of a hyphen,
// with spaces around the separator, or with a bare hour ("9:00-17:00"). All of
// those describe a perfectly good window, so accept them and store one
// spelling — zero-padded, hyphen-separated, no spaces — rather than silently
// dropping them at scheduling time.
func NormalizeRange(s string) (string, bool) {
	s = strings.TrimSpace(s)
	for _, dash := range []string{"–", "—", "−"} {
		s = strings.ReplaceAll(s, dash, "-")
	}
	start, end, found := strings.Cut(s, "-")
	if !found {
		return "", false
	}
	sh, sm, ok := parseHM(start)
	if !ok {
		return "", false
	}
	eh, em, ok := parseHM(end)
	if !ok {
		return "", false
	}
	if sh*60+sm >= eh*60+em {
		return "", false
	}
	return pad(sh) + ":" + pad(sm) + "-" + pad(eh) + ":" + pad(em), true
}

// NormalizeRanges canonicalizes a comma-separated list of ranges. The second
// return is the first entry that could not be parsed, so the caller can name
// it in an error rather than reporting a bare failure.
func NormalizeRanges(csv string) ([]string, string) {
	var out []string
	for _, part := range strings.Split(csv, ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		norm, ok := NormalizeRange(part)
		if !ok {
			return nil, strings.TrimSpace(part)
		}
		out = append(out, norm)
	}
	return out, ""
}

// parseHM reads "H:MM" or "HH:MM" in 24h form. A bare hour is not accepted:
// "9" is as likely to be a typo as a time, and the placeholder shows the full
// spelling.
func parseHM(s string) (int, int, bool) {
	hh, mm, found := strings.Cut(strings.TrimSpace(s), ":")
	if !found {
		return 0, 0, false
	}
	h, err := strconv.Atoi(strings.TrimSpace(hh))
	if err != nil || h < 0 || h > 23 {
		return 0, 0, false
	}
	m, err := strconv.Atoi(strings.TrimSpace(mm))
	if err != nil || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

func pad(v int) string {
	if v < 10 {
		return "0" + strconv.Itoa(v)
	}
	return strconv.Itoa(v)
}
