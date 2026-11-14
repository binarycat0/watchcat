package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/binarycat0/watchcat/internal/templates"
)

type templateText struct {
	Text string `json:"text"`
}

// templateRoutes serves the message templates edited in Config > Templates.
func (s *Server) templateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/templates", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.Templates.List())
	})
	mux.HandleFunc("GET /api/v1/templates/{name}", func(w http.ResponseWriter, r *http.Request) {
		writeTemplate(w)(s.Templates.Get(r.PathValue("name")))
	})
	mux.HandleFunc("PUT /api/v1/templates/{name}", func(w http.ResponseWriter, r *http.Request) {
		var req templateText
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{"invalid JSON body"})
			return
		}
		writeTemplate(w)(s.Templates.Save(r.PathValue("name"), req.Text))
	})
	// Restore the default: deletes the override file.
	mux.HandleFunc("DELETE /api/v1/templates/{name}", func(w http.ResponseWriter, r *http.Request) {
		writeTemplate(w)(s.Templates.Restore(r.PathValue("name")))
	})
	// Render text with sample data, without saving it.
	mux.HandleFunc("POST /api/v1/templates/{name}/preview", func(w http.ResponseWriter, r *http.Request) {
		var req templateText
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{"invalid JSON body"})
			return
		}
		out, err := s.Templates.Preview(r.PathValue("name"), req.Text)
		if err != nil {
			writeTemplateErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"output": out})
	})
}

func writeTemplate(w http.ResponseWriter) func(templates.Info, error) {
	return func(i templates.Info, err error) {
		if err != nil {
			writeTemplateErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, i)
	}
}

func writeTemplateErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, templates.ErrUnknown):
		writeJSON(w, http.StatusNotFound, errorResponse{err.Error()})
	case errors.Is(err, templates.ErrInvalid):
		writeJSON(w, http.StatusBadRequest, errorResponse{err.Error()})
	default:
		writeErr(w, err)
	}
}
