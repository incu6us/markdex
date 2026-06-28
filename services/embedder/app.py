import os

from fastapi import FastAPI
from fastapi.responses import JSONResponse
from pydantic import BaseModel

EMBED_MODEL = os.getenv("EMBED_MODEL", "BAAI/bge-m3")
RERANK_MODEL = os.getenv("RERANK_MODEL", "BAAI/bge-reranker-v2-m3")
DENSE_NAME = os.getenv("DENSE_NAME", "bge-m3-dense")
SPARSE_NAME = os.getenv("SPARSE_NAME", "bge-m3-sparse")
DENSE_DIM = int(os.getenv("DENSE_DIM", "1024"))
MAX_LENGTH = int(os.getenv("MAX_LENGTH", "8192"))
RERANK_MAX_LENGTH = int(os.getenv("RERANK_MAX_LENGTH", "512"))
USE_FP16 = os.getenv("USE_FP16", "true").lower() == "true"

_state: dict = {}

app = FastAPI(title="markdex embedder")


@app.on_event("startup")
def _load_models() -> None:
    # Imported lazily so the module loads (and tooling can import it) without the
    # heavy ML dependencies present.
    import torch
    from FlagEmbedding import BGEM3FlagModel
    from transformers import AutoModelForSequenceClassification, AutoTokenizer

    _state["embedder"] = BGEM3FlagModel(EMBED_MODEL, use_fp16=USE_FP16)

    # Reranking via transformers directly (the bge-reranker model-card pattern) — avoids a
    # FlagReranker/transformers slow-tokenizer incompatibility and uses the fast tokenizer.
    reranker = AutoModelForSequenceClassification.from_pretrained(RERANK_MODEL)
    reranker.eval()
    _state["torch"] = torch
    _state["rerank_tokenizer"] = AutoTokenizer.from_pretrained(RERANK_MODEL)
    _state["rerank_model"] = reranker


class EmbedRequest(BaseModel):
    texts: list[str]
    kind: str = "document"  # "document" | "query"; BGE-M3 is symmetric, kept for API stability


class SparseVector(BaseModel):
    indices: list[int]
    values: list[float]


class EmbedResponse(BaseModel):
    dense: list[list[float]]
    sparse: list[SparseVector]


class RerankRequest(BaseModel):
    query: str
    documents: list[str]
    top_k: int | None = None


@app.get("/healthz")
def healthz():
    if "embedder" not in _state or "rerank_model" not in _state:
        return JSONResponse(status_code=503, content={"status": "loading"})
    return {"status": "ok"}


@app.get("/info")
def info():
    return {
        "dense_dim": DENSE_DIM,
        "dense_name": DENSE_NAME,
        "sparse_name": SPARSE_NAME,
        "embed_model": EMBED_MODEL,
        "rerank_model": RERANK_MODEL,
    }


@app.post("/embed", response_model=EmbedResponse)
def embed(req: EmbedRequest):
    if not req.texts:
        return {"dense": [], "sparse": []}

    out = _state["embedder"].encode(
        req.texts,
        max_length=MAX_LENGTH,
        return_dense=True,
        return_sparse=True,
        return_colbert_vecs=False,
    )

    dense = [vec.tolist() for vec in out["dense_vecs"]]
    sparse = [
        {"indices": [int(token) for token in weights], "values": [float(w) for w in weights.values()]}
        for weights in out["lexical_weights"]
    ]
    return {"dense": dense, "sparse": sparse}


@app.post("/rerank")
def rerank(req: RerankRequest):
    if not req.documents:
        return {"results": []}

    torch = _state["torch"]
    tokenizer = _state["rerank_tokenizer"]
    model = _state["rerank_model"]

    pairs = [[req.query, doc] for doc in req.documents]
    inputs = tokenizer(
        pairs,
        padding=True,
        truncation=True,
        max_length=RERANK_MAX_LENGTH,
        return_tensors="pt",
    )
    with torch.no_grad():
        logits = model(**inputs).logits.view(-1).float()
        scores = torch.sigmoid(logits).tolist()

    ranked = sorted(enumerate(scores), key=lambda pair: pair[1], reverse=True)
    if req.top_k is not None:
        ranked = ranked[: req.top_k]
    return {"results": [{"index": index, "score": float(score)} for index, score in ranked]}
