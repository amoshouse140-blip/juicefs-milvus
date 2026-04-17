package api

type SearchResult struct {
	ID       string  `json:"id"`
	Score    float32 `json:"score"`
	Metadata []byte  `json:"metadata,omitempty"`
}

type CreateCollectionRequest struct {
	Name   string `json:"name"`
	Dim    int    `json:"dim"`
	Metric string `json:"metric"`
}

type HasCollectionResponse struct {
	Exists bool `json:"exists"`
}

type CreateIndexRequest struct {
	Name        string `json:"name"`
	VectorCount int64  `json:"vectorCount"`
	Metric      string `json:"metric"`
}

type ModifyVectorsRequest struct {
	Name         string      `json:"name"`
	IDs          []string    `json:"ids"`
	Vectors      [][]float32 `json:"vectors,omitempty"`
	MetadataJSON [][]byte    `json:"metadataJSON,omitempty"`
	Timestamps   []int64     `json:"timestamps,omitempty"`
}

type SearchRequest struct {
	Name   string    `json:"name"`
	Vector []float32 `json:"vector"`
	TopK   int       `json:"topK"`
	Nprobe int       `json:"nprobe"`
	Filter string    `json:"filter,omitempty"`
	Metric string    `json:"metric"`
}

type SearchResponse struct {
	Results []SearchResult `json:"results"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}
