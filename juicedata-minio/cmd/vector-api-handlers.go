package cmd

import (
	"encoding/json"
	"io"
	"net/http"

	xhttp "github.com/minio/minio/cmd/http"
)

const vectorAPIDefaultRegion = "us-east-1"
const vectorAPIMaxRequestBodySize = 20 << 20

func (api objectAPIHandlers) vectorAPI() VectorAPI {
	objectAPI := api.ObjectAPI()
	if objectAPI == nil {
		return nil
	}
	ext, ok := objectAPI.(VectorAPI)
	if !ok {
		return nil
	}
	return ext
}

func vectorRequestContext(r *http.Request) RequestContext {
	region := globalServerRegion
	if region == "" {
		region = vectorAPIDefaultRegion
	}
	return RequestContext{
		AccountID: r.Header.Get("X-Amz-Account-Id"),
		Region:    region,
		RequestID: r.Header.Get(xhttp.AmzRequestID),
		Headers:   r.Header.Clone(),
	}
}

func decodeVectorRequest(r *http.Request, dst interface{}) error {
	return json.NewDecoder(io.LimitReader(r.Body, vectorAPIMaxRequestBodySize)).Decode(dst)
}

func writeVectorSuccess(w http.ResponseWriter, resp interface{}) {
	if resp == nil {
		writeSuccessNoContent(w)
		return
	}
	data, err := json.Marshal(resp)
	if err != nil {
		writeVectorError(w, VectorAPIError{Code: "InternalServerException", Message: err.Error(), Status: http.StatusInternalServerError})
		return
	}
	writeSuccessResponseJSON(w, data)
}

func writeVectorError(w http.ResponseWriter, err error) {
	apiErr, ok := err.(VectorAPIError)
	if !ok {
		apiErr = VectorAPIError{Code: "InternalServerException", Message: err.Error(), Status: http.StatusInternalServerError}
	}
	if apiErr.Status == 0 {
		apiErr.Status = http.StatusInternalServerError
	}
	if apiErr.RetryAfter > 0 {
		w.Header().Set(xhttp.RetryAfter, "5")
	}
	data, marshalErr := json.Marshal(apiErr)
	if marshalErr != nil {
		apiErr = VectorAPIError{Code: "InternalServerException", Message: marshalErr.Error(), Status: http.StatusInternalServerError}
		data, _ = json.Marshal(apiErr)
	}
	writeResponse(w, apiErr.Status, data, mimeJSON)
}

func (api objectAPIHandlers) CreateVectorBucketHandler(w http.ResponseWriter, r *http.Request) {
	ext := api.vectorAPI()
	if ext == nil {
		writeVectorError(w, VectorAPIError{Code: "NotImplementedException", Message: "vector bucket api is not configured", Status: http.StatusNotImplemented})
		return
	}
	var req CreateVectorBucketRequest
	if err := decodeVectorRequest(r, &req); err != nil {
		writeVectorError(w, VectorAPIError{Code: "ValidationException", Message: err.Error(), Status: http.StatusBadRequest})
		return
	}
	req.RequestContext = vectorRequestContext(r)
	resp, err := ext.CreateVectorBucket(r.Context(), &req)
	if err != nil {
		writeVectorError(w, err)
		return
	}
	writeVectorSuccess(w, resp)
}

func (api objectAPIHandlers) GetVectorBucketHandler(w http.ResponseWriter, r *http.Request) {
	ext := api.vectorAPI()
	if ext == nil {
		writeVectorError(w, VectorAPIError{Code: "NotImplementedException", Message: "vector bucket api is not configured", Status: http.StatusNotImplemented})
		return
	}
	var req GetVectorBucketRequest
	if err := decodeVectorRequest(r, &req); err != nil {
		writeVectorError(w, VectorAPIError{Code: "ValidationException", Message: err.Error(), Status: http.StatusBadRequest})
		return
	}
	req.RequestContext = vectorRequestContext(r)
	resp, err := ext.GetVectorBucket(r.Context(), &req)
	if err != nil {
		writeVectorError(w, err)
		return
	}
	writeVectorSuccess(w, resp)
}

func (api objectAPIHandlers) ListVectorBucketsHandler(w http.ResponseWriter, r *http.Request) {
	ext := api.vectorAPI()
	if ext == nil {
		writeVectorError(w, VectorAPIError{Code: "NotImplementedException", Message: "vector bucket api is not configured", Status: http.StatusNotImplemented})
		return
	}
	var req ListVectorBucketsRequest
	if err := decodeVectorRequest(r, &req); err != nil {
		writeVectorError(w, VectorAPIError{Code: "ValidationException", Message: err.Error(), Status: http.StatusBadRequest})
		return
	}
	req.RequestContext = vectorRequestContext(r)
	resp, err := ext.ListVectorBuckets(r.Context(), &req)
	if err != nil {
		writeVectorError(w, err)
		return
	}
	writeVectorSuccess(w, resp)
}

func (api objectAPIHandlers) DeleteVectorBucketHandler(w http.ResponseWriter, r *http.Request) {
	ext := api.vectorAPI()
	if ext == nil {
		writeVectorError(w, VectorAPIError{Code: "NotImplementedException", Message: "vector bucket api is not configured", Status: http.StatusNotImplemented})
		return
	}
	var req DeleteVectorBucketRequest
	if err := decodeVectorRequest(r, &req); err != nil {
		writeVectorError(w, VectorAPIError{Code: "ValidationException", Message: err.Error(), Status: http.StatusBadRequest})
		return
	}
	req.RequestContext = vectorRequestContext(r)
	if err := ext.DeleteVectorBucket(r.Context(), &req); err != nil {
		writeVectorError(w, err)
		return
	}
	writeVectorSuccess(w, nil)
}

func (api objectAPIHandlers) CreateIndexHandler(w http.ResponseWriter, r *http.Request) {
	ext := api.vectorAPI()
	if ext == nil {
		writeVectorError(w, VectorAPIError{Code: "NotImplementedException", Message: "vector bucket api is not configured", Status: http.StatusNotImplemented})
		return
	}
	var req CreateIndexRequest
	if err := decodeVectorRequest(r, &req); err != nil {
		writeVectorError(w, VectorAPIError{Code: "ValidationException", Message: err.Error(), Status: http.StatusBadRequest})
		return
	}
	req.RequestContext = vectorRequestContext(r)
	resp, err := ext.CreateIndex(r.Context(), &req)
	if err != nil {
		writeVectorError(w, err)
		return
	}
	writeVectorSuccess(w, resp)
}

func (api objectAPIHandlers) DeleteIndexHandler(w http.ResponseWriter, r *http.Request) {
	ext := api.vectorAPI()
	if ext == nil {
		writeVectorError(w, VectorAPIError{Code: "NotImplementedException", Message: "vector bucket api is not configured", Status: http.StatusNotImplemented})
		return
	}
	var req DeleteIndexRequest
	if err := decodeVectorRequest(r, &req); err != nil {
		writeVectorError(w, VectorAPIError{Code: "ValidationException", Message: err.Error(), Status: http.StatusBadRequest})
		return
	}
	req.RequestContext = vectorRequestContext(r)
	if err := ext.DeleteIndex(r.Context(), &req); err != nil {
		writeVectorError(w, err)
		return
	}
	writeVectorSuccess(w, nil)
}

func (api objectAPIHandlers) PutVectorsHandler(w http.ResponseWriter, r *http.Request) {
	ext := api.vectorAPI()
	if ext == nil {
		writeVectorError(w, VectorAPIError{Code: "NotImplementedException", Message: "vector bucket api is not configured", Status: http.StatusNotImplemented})
		return
	}
	var req PutVectorsRequest
	if err := decodeVectorRequest(r, &req); err != nil {
		writeVectorError(w, VectorAPIError{Code: "ValidationException", Message: err.Error(), Status: http.StatusBadRequest})
		return
	}
	req.RequestContext = vectorRequestContext(r)
	if err := ext.PutVectors(r.Context(), &req); err != nil {
		writeVectorError(w, err)
		return
	}
	writeVectorSuccess(w, nil)
}

func (api objectAPIHandlers) DeleteVectorsHandler(w http.ResponseWriter, r *http.Request) {
	ext := api.vectorAPI()
	if ext == nil {
		writeVectorError(w, VectorAPIError{Code: "NotImplementedException", Message: "vector bucket api is not configured", Status: http.StatusNotImplemented})
		return
	}
	var req DeleteVectorsRequest
	if err := decodeVectorRequest(r, &req); err != nil {
		writeVectorError(w, VectorAPIError{Code: "ValidationException", Message: err.Error(), Status: http.StatusBadRequest})
		return
	}
	req.RequestContext = vectorRequestContext(r)
	if err := ext.DeleteVectors(r.Context(), &req); err != nil {
		writeVectorError(w, err)
		return
	}
	writeVectorSuccess(w, nil)
}

func (api objectAPIHandlers) QueryVectorsHandler(w http.ResponseWriter, r *http.Request) {
	ext := api.vectorAPI()
	if ext == nil {
		writeVectorError(w, VectorAPIError{Code: "NotImplementedException", Message: "vector bucket api is not configured", Status: http.StatusNotImplemented})
		return
	}
	var req QueryVectorsRequest
	if err := decodeVectorRequest(r, &req); err != nil {
		writeVectorError(w, VectorAPIError{Code: "ValidationException", Message: err.Error(), Status: http.StatusBadRequest})
		return
	}
	req.RequestContext = vectorRequestContext(r)
	resp, err := ext.QueryVectors(r.Context(), &req)
	if err != nil {
		writeVectorError(w, err)
		return
	}
	writeVectorSuccess(w, resp)
}
