package timeutil

import "time"

const LocationName = "Asia/Shanghai"

var businessLocation = loadBusinessLocation()

func loadBusinessLocation() *time.Location {
	location, err := time.LoadLocation(LocationName)
	if err != nil {
		return time.FixedZone(LocationName, 8*60*60)
	}
	return location
}

func Location() *time.Location {
	return businessLocation
}

func Now() time.Time {
	return time.Now().In(businessLocation)
}

func InBusinessLocation(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.In(businessLocation)
}

func FormatRFC3339Nano(t time.Time) string {
	return InBusinessLocation(t).Format(time.RFC3339Nano)
}
