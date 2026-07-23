package domain

type CollectionSchema struct {
	DenseDimension int
	DenseVector    string
	SparseVector   string
	// QuantBits, when >0, enables TurboQuant scalar quantization on the dense vector
	// (value is rendered as Qdrant's "bits{N}", e.g. 2 -> "bits2"). 0 = off.
	QuantBits int
	// QuantOnDisk stores the float32 dense originals on disk (quantized copy stays in
	// RAM). Required for the RAM savings — without it Qdrant keeps both copies in RAM.
	QuantOnDisk bool
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
