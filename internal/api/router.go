// Package api wires the HTTP routes. The same router serves locally and on Lambda.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"notes-app/internal/photos"
	"notes-app/internal/store"
)

type Server struct {
	Store  *store.Store
	Photos *photos.Photos
}

func NewRouter(s *Server, allowedOrigin string) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, logRequests, middleware.Recoverer, cors(allowedOrigin))

	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok-docker"})
	})
	r.Route("/folders", func(r chi.Router) {
		r.Post("/", s.createFolder)
		r.Get("/", s.listFolders)
		r.Delete("/{folderId}", s.deleteFolder)
		r.Post("/{folderId}/notes", s.createNote)
		r.Get("/{folderId}/notes", s.listNotes)
		r.Put("/{folderId}/notes/{noteId}", s.updateNote)
		r.Delete("/{folderId}/notes/{noteId}", s.deleteNote)
		r.Post("/{folderId}/notes/{noteId}/photo-url", s.photoUploadURL)
		r.Post("/{folderId}/notes/{noteId}/photos", s.confirmPhoto)
	})
	return r
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// fail maps store errors to HTTP status codes.
func fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	slog.ErrorContext(r.Context(), "internal error", "requestId", middleware.GetReqID(r.Context()), "err", err.Error())
	writeErr(w, http.StatusInternalServerError, "internal error")
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB cap on JSON bodies
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

// ---- folders ----

func (s *Server) createFolder(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name string }
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 100 {
		writeErr(w, http.StatusBadRequest, "name is required (max 100 chars)")
		return
	}
	f, err := s.Store.CreateFolder(r.Context(), in.Name)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, f)
}

func (s *Server) listFolders(w http.ResponseWriter, r *http.Request) {
	fs, err := s.Store.ListFolders(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, fs)
}

func (s *Server) deleteFolder(w http.ResponseWriter, r *http.Request) {
	keys, err := s.Store.DeleteFolder(r.Context(), chi.URLParam(r, "folderId"))
	if err != nil {
		fail(w, r, err)
		return
	}
	s.deletePhotos(r, keys)
	w.WriteHeader(http.StatusNoContent)
}

// ---- notes ----

type noteInput struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

func (n *noteInput) valid() bool {
	n.Title = strings.TrimSpace(n.Title)
	return n.Title != "" && len(n.Title) <= 200 && len(n.Text) <= 100_000
}

func (s *Server) createNote(w http.ResponseWriter, r *http.Request) {
	var in noteInput
	if !decode(w, r, &in) {
		return
	}
	if !in.valid() {
		writeErr(w, http.StatusBadRequest, "title is required (max 200 chars); text max 100k chars")
		return
	}
	n, err := s.Store.CreateNote(r.Context(), chi.URLParam(r, "folderId"), in.Title, in.Text)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, n)
}

func (s *Server) listNotes(w http.ResponseWriter, r *http.Request) {
	folderID := chi.URLParam(r, "folderId")
	ok, err := s.Store.FolderExists(r.Context(), folderID)
	if err != nil {
		fail(w, r, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	ns, err := s.Store.ListNotes(r.Context(), folderID)
	if err != nil {
		fail(w, r, err)
		return
	}
	out := make([]noteOut, 0, len(ns))
	for _, n := range ns {
		o := noteOut{Note: n, Photos: []photoOut{}}
		for _, k := range n.PhotoKeys {
			u, err := s.Photos.ViewURL(r.Context(), k)
			if err != nil {
				fail(w, r, err)
				return
			}
			o.Photos = append(o.Photos, photoOut{Key: k, URL: u})
		}
		out = append(out, o)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) updateNote(w http.ResponseWriter, r *http.Request) {
	var in noteInput
	if !decode(w, r, &in) {
		return
	}
	if !in.valid() {
		writeErr(w, http.StatusBadRequest, "title is required (max 200 chars); text max 100k chars")
		return
	}
	n, err := s.Store.UpdateNote(r.Context(), chi.URLParam(r, "folderId"), chi.URLParam(r, "noteId"), in.Title, in.Text)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) deleteNote(w http.ResponseWriter, r *http.Request) {
	keys, err := s.Store.DeleteNote(r.Context(), chi.URLParam(r, "folderId"), chi.URLParam(r, "noteId"))
	if err != nil {
		fail(w, r, err)
		return
	}
	s.deletePhotos(r, keys)
	w.WriteHeader(http.StatusNoContent)
}

// ---- photos ----

type photoOut struct {
	Key string `json:"key"`
	URL string `json:"url"` // presigned GET, valid 1 hour
}

type noteOut struct {
	store.Note
	Photos []photoOut `json:"photos"`
}

// photoUploadURL: step 1 of an upload. Returns a presigned PUT URL + the key to confirm later.
func (s *Server) photoUploadURL(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ContentType string `json:"contentType"`
		Size        int64  `json:"size"`
	}
	if !decode(w, r, &in) {
		return
	}
	noteID := chi.URLParam(r, "noteId")
	if _, err := s.Store.GetNote(r.Context(), chi.URLParam(r, "folderId"), noteID); err != nil {
		fail(w, r, err)
		return
	}
	url, key, err := s.Photos.UploadURL(r.Context(), noteID, in.ContentType, in.Size)
	if errors.Is(err, photos.ErrBadType) || errors.Is(err, photos.ErrTooBig) {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"uploadUrl": url, "key": key, "contentType": in.ContentType})
}

// confirmPhoto: step 2, after the browser PUT the file to S3. Saves the key on the note.
func (s *Server) confirmPhoto(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key string `json:"key"`
	}
	if !decode(w, r, &in) {
		return
	}
	folderID, noteID := chi.URLParam(r, "folderId"), chi.URLParam(r, "noteId")
	// Only accept keys under this note's prefix, so a client can't attach someone else's object.
	if !strings.HasPrefix(in.Key, photos.KeyPrefix(noteID)) || strings.Contains(in.Key, "..") {
		writeErr(w, http.StatusBadRequest, "key does not belong to this note")
		return
	}
	ok, err := s.Photos.Exists(r.Context(), in.Key)
	if err != nil {
		fail(w, r, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusBadRequest, "photo not found in S3; upload it first")
		return
	}
	n, err := s.Store.AddPhoto(r.Context(), folderID, noteID, in.Key)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

// deletePhotos removes S3 objects after their DB rows are gone. Best effort: a failure
// is logged (orphaned objects cost almost nothing) rather than failing the request.
func (s *Server) deletePhotos(r *http.Request, keys []string) {
	if err := s.Photos.Delete(r.Context(), keys); err != nil {
		slog.ErrorContext(r.Context(), "photo cleanup failed", "requestId", middleware.GetReqID(r.Context()), "keys", keys, "err", err.Error())
	}
}
