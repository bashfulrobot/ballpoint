package sources

import "strconv"

// NormalizePriority converts Todoist's inverted API priority, where 4 is
// highest, into the p1 through p4 band the rest of ballpoint uses, where p1 is
// highest. Values outside 1 through 4 are clamped so a caller never sees an
// impossible band. Normalisation lives here so no caller ever handles the raw
// integer.
func NormalizePriority(api int) string {
	if api < 1 {
		api = 1
	}
	if api > 4 {
		api = 4
	}
	return "p" + strconv.Itoa(5-api)
}
