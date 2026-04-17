package adapter

import (
	"fmt"
	"math"
)

func computeNlist(vectorCount int64) int {
	nlist := int(math.Sqrt(float64(vectorCount)) * 4)
	if nlist < 1024 {
		nlist = 1024
	}
	if nlist > 65536 {
		nlist = 65536
	}
	return nlist
}

func normalizeMetric(metric string) string {
	switch metric {
	case "L2":
		return "L2"
	case "COSINE":
		return "COSINE"
	default:
		return "COSINE"
	}
}

func buildIDFilter(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	filter := `id in [`
	for i, id := range ids {
		if i > 0 {
			filter += ","
		}
		filter += fmt.Sprintf("%q", id)
	}
	filter += `]`
	return filter
}
