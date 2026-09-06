package hours

import "strings"

// Zone is one entry in the time-zone picker.
type Zone struct {
	Name    string // IANA identifier, e.g. "America/Chicago"
	Region  string // optgroup heading, e.g. "Americas"
	Label   string // the city on its own, e.g. "Chicago"
	Display string // what the option reads as, e.g. "Chicago — America/Chicago"
}

// ZoneGroup is one optgroup: a region and the zones filed under it, in the
// order they should appear.
type ZoneGroup struct {
	Region string
	Zones  []Zone
}

// zoneNames is a curated subset of the IANA database — the zones a single
// person running skulid on their own box plausibly lives or works in. It is
// deliberately not the full ~600-entry list: a picker nobody can scan is no
// better than the free-text box it replaced. Anything already stored that is
// missing here is preserved by ZoneGroupsWith, so a hand-set exotic zone
// survives a round trip through the form.
var zoneNames = []string{
	"UTC",

	"America/Adak", "America/Anchorage", "America/Los_Angeles", "America/Vancouver",
	"America/Tijuana", "America/Phoenix", "America/Denver", "America/Edmonton",
	"America/Chihuahua", "America/Chicago", "America/Winnipeg", "America/Mexico_City",
	"America/Guatemala", "America/Regina", "America/New_York", "America/Toronto",
	"America/Detroit", "America/Havana", "America/Panama", "America/Bogota",
	"America/Lima", "America/Halifax", "America/Puerto_Rico", "America/Santiago",
	"America/Caracas", "America/St_Johns", "America/Sao_Paulo",
	"America/Argentina/Buenos_Aires", "America/Montevideo", "America/Noronha",
	"Pacific/Honolulu",

	"Europe/London", "Europe/Dublin", "Europe/Lisbon", "Atlantic/Reykjavik",
	"Europe/Amsterdam", "Europe/Berlin", "Europe/Brussels", "Europe/Budapest",
	"Europe/Copenhagen", "Europe/Madrid", "Europe/Oslo", "Europe/Paris",
	"Europe/Prague", "Europe/Rome", "Europe/Stockholm", "Europe/Vienna",
	"Europe/Warsaw", "Europe/Zurich", "Europe/Athens", "Europe/Bucharest",
	"Europe/Helsinki", "Europe/Kyiv", "Europe/Riga", "Europe/Sofia",
	"Europe/Tallinn", "Europe/Vilnius", "Europe/Istanbul", "Europe/Moscow",

	"Africa/Casablanca", "Africa/Lagos", "Africa/Algiers", "Africa/Cairo",
	"Africa/Johannesburg", "Africa/Nairobi", "Africa/Accra", "Africa/Tunis",

	"Asia/Jerusalem", "Asia/Beirut", "Asia/Riyadh", "Asia/Baghdad", "Asia/Tehran",
	"Asia/Dubai", "Asia/Karachi", "Asia/Kolkata", "Asia/Kathmandu", "Asia/Dhaka",
	"Asia/Colombo", "Asia/Yangon", "Asia/Bangkok", "Asia/Jakarta", "Asia/Ho_Chi_Minh",
	"Asia/Shanghai", "Asia/Hong_Kong", "Asia/Singapore", "Asia/Kuala_Lumpur",
	"Asia/Taipei", "Asia/Manila", "Asia/Seoul", "Asia/Tokyo",

	"Australia/Perth", "Australia/Adelaide", "Australia/Darwin", "Australia/Brisbane",
	"Australia/Sydney", "Australia/Melbourne", "Australia/Hobart",
	"Pacific/Auckland", "Pacific/Fiji", "Pacific/Guam", "Pacific/Port_Moresby",
	"Pacific/Chatham",
}

// regionFor maps an IANA area prefix onto the heading the picker groups it
// under. The areas are collapsed — Atlantic/Reykjavik belongs next to Dublin,
// not in a group of its own — because the point is "find your city fast", not
// a faithful rendering of the database's layout.
func regionFor(name string) string {
	area, _, found := strings.Cut(name, "/")
	if !found {
		return "Universal"
	}
	switch area {
	case "America":
		return "Americas"
	case "Europe":
		return "Europe"
	case "Africa":
		return "Africa"
	case "Asia":
		return "Asia & Middle East"
	case "Australia":
		return "Australia & Pacific"
	case "Atlantic":
		return "Europe"
	case "Pacific":
		return "Australia & Pacific"
	case "Indian":
		return "Asia & Middle East"
	}
	return "Other"
}

// regionOrder fixes the optgroup order. Anything whose region isn't listed
// sorts to the end, which is where a preserved unknown zone belongs.
var regionOrder = []string{
	"Universal", "Americas", "Europe", "Africa",
	"Asia & Middle East", "Australia & Pacific", "Other",
}

// labelFor renders the city half of an IANA name for display:
// "America/Argentina/Buenos_Aires" -> "Buenos Aires".
func labelFor(name string) string {
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	return strings.ReplaceAll(name, "_", " ")
}

// displayFor leads with the city so a native select's type-ahead matches what
// people actually know ("chi" finds Chicago), then names the zone in full so
// there is no doubt which one was picked.
func displayFor(name string) string {
	label := labelFor(name)
	if label == name {
		return name
	}
	return label + " — " + name
}

// Known reports whether name is in the curated list.
func Known(name string) bool {
	for _, z := range zoneNames {
		if z == name {
			return true
		}
	}
	return false
}

// ZoneGroupsWith returns the picker's optgroups, guaranteeing that current is
// among them. A zone set by hand or carried over from Google that isn't in the
// curated list would otherwise vanish the first time someone saved an
// unrelated field on the same form.
func ZoneGroupsWith(current string) []ZoneGroup {
	names := zoneNames
	if current = strings.TrimSpace(current); current != "" && !Known(current) {
		names = append(append([]string{}, zoneNames...), current)
	}

	byRegion := map[string][]Zone{}
	for _, n := range names {
		r := regionFor(n)
		byRegion[r] = append(byRegion[r], Zone{
			Name: n, Region: r, Label: labelFor(n), Display: displayFor(n),
		})
	}

	out := make([]ZoneGroup, 0, len(regionOrder))
	for _, r := range regionOrder {
		if zs := byRegion[r]; len(zs) > 0 {
			out = append(out, ZoneGroup{Region: r, Zones: zs})
		}
	}
	return out
}
