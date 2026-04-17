package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/juicedata/juicefs-milvus/milvus/bridge/internal/api"
)

type vectorService interface {
	CreateCollection(context.Context, api.CreateCollectionRequest) error
	DropCollection(context.Context, string) error
	HasCollection(context.Context, string) (bool, error)
	CreateIndex(context.Context, api.CreateIndexRequest) error
	LoadCollection(context.Context, string) error
	ReleaseCollection(context.Context, string) error
	Insert(context.Context, api.ModifyVectorsRequest) error
	Upsert(context.Context, api.ModifyVectorsRequest) error
	Delete(context.Context, api.ModifyVectorsRequest) error
	Search(context.Context, api.SearchRequest) ([]api.SearchResult, error)
}

func NewHandler(svc vectorService) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/collections", func(w http.ResponseWriter, r *http.Request) {
		var req api.CreateCollectionRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		run(w, r, func(ctx context.Context) error {
			return svc.CreateCollection(ctx, req)
		})
	})
	mux.HandleFunc("DELETE /v1/collections/", func(w http.ResponseWriter, r *http.Request) {
		name, ok := collectionNameFromPath(w, r.URL.Path, "")
		if !ok {
			return
		}
		run(w, r, func(ctx context.Context) error {
			return svc.DropCollection(ctx, name)
		})
	})
	mux.HandleFunc("GET /v1/collections/", func(w http.ResponseWriter, r *http.Request) {
		name, ok := collectionNameFromPath(w, r.URL.Path, "")
		if !ok {
			return
		}
		exists, err := svc.HasCollection(r.Context(), name)
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, api.HasCollectionResponse{Exists: exists})
	})
	mux.HandleFunc("POST /v1/collections/", func(w http.ResponseWriter, r *http.Request) {
		name, suffix, ok := collectionNameAndActionFromPath(w, r.URL.Path)
		if !ok {
			return
		}
		switch suffix {
		case "load":
			run(w, r, func(ctx context.Context) error {
				return svc.LoadCollection(ctx, name)
			})
		case "release":
			run(w, r, func(ctx context.Context) error {
				return svc.ReleaseCollection(ctx, name)
			})
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("POST /v1/indexes", func(w http.ResponseWriter, r *http.Request) {
		var req api.CreateIndexRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		run(w, r, func(ctx context.Context) error {
			return svc.CreateIndex(ctx, req)
		})
	})
	mux.HandleFunc("POST /v1/vectors/insert", func(w http.ResponseWriter, r *http.Request) {
		var req api.ModifyVectorsRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		run(w, r, func(ctx context.Context) error {
			return svc.Insert(ctx, req)
		})
	})
	mux.HandleFunc("POST /v1/vectors/upsert", func(w http.ResponseWriter, r *http.Request) {
		var req api.ModifyVectorsRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		run(w, r, func(ctx context.Context) error {
			return svc.Upsert(ctx, req)
		})
	})
	mux.HandleFunc("POST /v1/vectors/delete", func(w http.ResponseWriter, r *http.Request) {
		var req api.ModifyVectorsRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		run(w, r, func(ctx context.Context) error {
			return svc.Delete(ctx, req)
		})
	})
	mux.HandleFunc("POST /v1/vectors/search", func(w http.ResponseWriter, r *http.Request) {
		var req api.SearchRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		results, err := svc.Search(r.Context(), req)
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, api.SearchResponse{Results: results})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	return mux
}

func run(w http.ResponseWriter, r *http.Request, fn func(context.Context) error) {
	if err := fn(r.Context()); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, err error) {
	if err == nil {
		err = errors.New(http.StatusText(status))
	}
	writeJSON(w, status, api.ErrorResponse{Error: err.Error()})
}

func collectionNameFromPath(w http.ResponseWriter, path string, suffix string) (string, bool) {
	name, found := strings.CutPrefix(path, "/v1/collections/")
	if !found || name == "" {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return "", false
	}
	if suffix != "" {
		name = strings.TrimSuffix(name, "/"+suffix)
	}
	if name == "" {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return "", false
	}
	return name, true
}

func collectionNameAndActionFromPath(w http.ResponseWriter, path string) (string, string, bool) {
	rest, found := strings.CutPrefix(path, "/v1/collections/")
	if !found || rest == "" {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return "", "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return "", "", false
	}
	return parts[0], parts[1], true
}
