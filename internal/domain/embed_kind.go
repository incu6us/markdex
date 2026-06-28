package domain

type EmbedKind int

const (
	DocumentKind EmbedKind = iota
	QueryKind
)

func (k EmbedKind) String() string {
	if k == QueryKind {
		return "query"
	}
	return "document"
}
