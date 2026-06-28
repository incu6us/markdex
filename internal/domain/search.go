package domain

type CollectionSchema struct {
	DenseDimension int
	DenseVector    string
	SparseVector   string
}

type Filter struct {
	Match map[string]string
}

func (f Filter) IsEmpty() bool {
	return len(f.Match) == 0
}

type SearchHit struct {
	ID       string
	Score    float32
	Document string
	Metadata map[string]string
}
