package shadow

import "reflect"

// ComputeDelta returns the keys whose desired value differs from reported.
// For each desired key, if reported[k] != desired[k] (deep comparison via
// reflect.DeepEqual), include desired[k] in the delta. Keys present only in
// reported are ignored.
func ComputeDelta(reported, desired map[string]any) map[string]any {
	delta := make(map[string]any)
	for k, dv := range desired {
		rv, ok := reported[k]
		if !ok || !reflect.DeepEqual(rv, dv) {
			delta[k] = dv
		}
	}
	return delta
}
