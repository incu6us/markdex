package domain

import "strings"

// SourcesToPrune returns the source IDs in existing that fall under scopePrefix but
// are absent from kept — i.e. docs that no longer exist at the origin and whose
// chunks should be deleted (collection reconciliation). An empty scopePrefix prunes
// nothing, so reconciliation can never wipe an out-of-scope source by accident.
func SourcesToPrune(existing, kept []string, scopePrefix string) []string {
	if scopePrefix == "" {
		return nil
	}
	keptSet := make(map[string]struct{}, len(kept))
	for _, k := range kept {
		keptSet[k] = struct{}{}
	}

	var prune []string
	for _, e := range existing {
		if !strings.HasPrefix(e, scopePrefix) {
			continue
		}
		if _, ok := keptSet[e]; !ok {
			prune = append(prune, e)
		}
	}
	return prune
}
