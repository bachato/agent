package cleanup

// usedPercent calculates how full a filesystem is, given a total capacity and
// the amount available to the calling user. The inputs may be bytes (Windows)
// or blocks (Unix); the ratio is the same.
func usedPercent(total, available uint64) float64 {
	if total == 0 {
		return 0
	}
	used := total - available
	return float64(used) / float64(total) * 100
}
