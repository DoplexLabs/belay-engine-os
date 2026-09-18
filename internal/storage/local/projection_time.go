package local

import "time"

const projectionTimestampLayout = "2006-01-02T15:04:05.000000000Z"

func formatProjectionTime(value time.Time) string {
	return value.UTC().Format(projectionTimestampLayout)
}

func parseProjectionTime(value string) (time.Time, error) {
	if parsed, err := time.Parse(projectionTimestampLayout, value); err == nil {
		return parsed, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

func projectionOrderNS(value time.Time) (int64, bool) {
	if value.IsZero() {
		return 0, false
	}
	normalized := value.UTC()
	order := normalized.UnixNano()
	return order, time.Unix(0, order).UTC().Equal(normalized)
}
