package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"altpocket/internal/auth"
	"altpocket/internal/config"
	"altpocket/internal/logger"
	"altpocket/internal/ratelimit"
	"altpocket/internal/store"
	"altpocket/internal/tag"
	"altpocket/internal/ui"
	"altpocket/internal/urlnorm"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/idtoken"
	"log/slog"

	"github.com/jackc/pgx/v5"
)

type Server struct {
	cfg      config.Config
	store    *store.Store
	limiter  *ratelimit.Limiter
	logger   *slog.Logger
	renderer *ui.Renderer
	oauthCfg *oauth2.Config
}

func New(cfg config.Config, st *store.Store, limiter *ratelimit.Limiter, log *slog.Logger, renderer *ui.Renderer) *Server {
	return &Server{
		cfg:      cfg,
		store:    st,
		limiter:  limiter,
		logger:   log,
		renderer: renderer,
		oauthCfg: &oauth2.Config{
			ClientID:     cfg.GoogleWebClientID,
			ClientSecret: cfg.GoogleClientSecret,
			RedirectURL:  strings.TrimRight(cfg.PublicBaseURL, "/") + "/v1/auth/google/callback",
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint:     google.Endpoint,
		},
	}
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(RequestID)
	r.Use(AccessLog(s.logger))

	r.Get("/healthz", s.handleHealth)

	r.Route("/v1", func(r chi.Router) {
		r.Use(s.cors)
		r.Get("/auth/google/login", s.handleGoogleLogin)
		r.Get("/auth/google/callback", s.handleGoogleCallback)
		r.Post("/auth/extension/exchange", s.handleExtensionExchange)
		r.Get("/tags", s.requireAuth(s.handleTags))

		r.Route("/items", func(r chi.Router) {
			r.Get("/", s.requireAuth(s.handleListItems))
			r.Post("/", s.requireAuth(s.handleCreateItem))
			r.Get("/{id}", s.requireAuth(s.handleGetItem))
			r.Delete("/{id}", s.requireAuth(s.handleDeleteItem))
			r.Post("/{id}/refetch", s.requireAuth(s.handleRefetchItem))
		})
	})

	r.Route("/ui", func(r chi.Router) {
		r.Get("/items", s.requireWeb(s.handleUIItems))
		r.Get("/items/{id}", s.requireWeb(s.handleUIItem))
	})

	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	return r
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleGoogleLogin(w http.ResponseWriter, r *http.Request) {
	state, err := auth.RandomString(16)
	if err != nil {
		http.Error(w, "failed", http.StatusInternalServerError)
		return
	}
	cookie := &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   strings.HasPrefix(s.cfg.PublicBaseURL, "https://"),
		MaxAge:   300,
	}
	http.SetCookie(w, cookie)
	url := s.oauthCfg.AuthCodeURL(state, oauth2.AccessTypeOnline)
	http.Redirect(w, r, url, http.StatusFound)
}

func (s *Server) handleGoogleCallback(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("oauth_state")
	if err != nil || cookie.Value == "" || cookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	token, err := s.oauthCfg.Exchange(r.Context(), code)
	if err != nil {
		http.Error(w, "exchange failed", http.StatusBadRequest)
		return
	}
	idToken, ok := token.Extra("id_token").(string)
	if !ok || idToken == "" {
		http.Error(w, "missing id_token", http.StatusBadRequest)
		return
	}
	payload, err := idtoken.Validate(r.Context(), idToken, s.cfg.GoogleWebClientID)
	if err != nil {
		http.Error(w, "invalid id_token", http.StatusUnauthorized)
		return
	}

	sub := payload.Subject
	email, _ := payload.Claims["email"].(string)
	name, _ := payload.Claims["name"].(string)
	avatar, _ := payload.Claims["picture"].(string)

	user, err := s.store.UpsertUser(r.Context(), sub, email, name, avatar)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	csrf, err := auth.RandomString(24)
	if err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	sess, err := s.store.CreateSession(r.Context(), user.ID, csrf, config.SessionTTL())
	if err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "altpocket_session",
		Value:    sess.ID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   strings.HasPrefix(s.cfg.PublicBaseURL, "https://"),
		MaxAge:   int(config.SessionTTL().Seconds()),
	})
	// Clear oauth_state
	http.SetCookie(w, &http.Cookie{Name: "oauth_state", Value: "", Path: "/", MaxAge: -1})

	http.Redirect(w, r, "/ui/items", http.StatusFound)
}

func (s *Server) handleExtensionExchange(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDToken string `json:"id_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.IDToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	payload, err := idtoken.Validate(r.Context(), req.IDToken, s.cfg.GoogleExtClientID)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
		return
	}
	sub := payload.Subject
	user, err := s.store.GetUserBySub(r.Context(), sub)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "user_not_registered"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db_error"})
		return
	}

	token, exp, err := auth.IssueJWT(s.cfg.JWTSecret, user.ID, 24*time.Hour)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token_error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token":       token,
		"expires_in":  exp - time.Now().Unix(),
	})
}

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	norm := tag.Normalize(q)
	tags, err := s.store.SuggestTags(r.Context(), norm)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db_error"})
		return
	}
	writeJSON(w, http.StatusOK, tags)
}

func (s *Server) handleCreateItem(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !s.limiter.Allow(user.ID) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate_limited"})
		return
	}

	var req struct {
		URL  string   `json:"url"`
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}

	canonicalURL, canonicalHash, err := urlnorm.Canonicalize(req.URL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_url"})
		return
	}

	normTags := []string{}
	seen := map[string]struct{}{}
	for _, t := range req.Tags {
		norm := tag.Normalize(t)
		if norm != "" {
			if _, ok := seen[norm]; ok {
				continue
			}
			seen[norm] = struct{}{}
			normTags = append(normTags, norm)
		}
	}

	itemID, created, err := s.store.CreateItem(r.Context(), user.ID, req.URL, canonicalURL, canonicalHash, normTags)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db_error"})
		return
	}

	if created {
		s.logger.Info("items.create", slog.String("item_id", itemID), slog.Bool("created", true), slog.String("request_id", s.requestID(r.Context())))
	} else {
		s.logger.Info("items.create", slog.String("item_id", itemID), slog.Bool("created", false), slog.String("request_id", s.requestID(r.Context())))
		s.logger.Info("duplicate_noop", slog.String("item_id", itemID), slog.String("request_id", s.requestID(r.Context())))
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"item_id": itemID, "created": created})
}

func (s *Server) handleListItems(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	q := r.URL.Query().Get("q")
	tagFilter := tag.Normalize(r.URL.Query().Get("tag"))
	sort := defaultSort(r.URL.Query().Get("sort"))
	page := parseInt(r.URL.Query().Get("page"), 1)
	perPage := perPageValue(r.URL.Query().Get("per_page"))

	items, pag, err := s.store.ListItems(r.Context(), user.ID, page, perPage, q, tagFilter, sort)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db_error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"items": items, "pagination": pag})
}

func (s *Server) handleGetItem(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	id := chi.URLParam(r, "id")
	item, err := s.store.GetItemDetail(r.Context(), user.ID, id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleDeleteItem(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !s.limiter.Allow(user.ID) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate_limited"})
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.store.DeleteItem(r.Context(), user.ID, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db_error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRefetchItem(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if !s.limiter.Allow(user.ID) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate_limited"})
		return
	}
	id := chi.URLParam(r, "id")
	if err := s.store.RequestRefetch(r.Context(), user.ID, id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	s.logger.Info("refetch_requested", slog.String("item_id", id), slog.String("request_id", s.requestID(r.Context())))
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
}

func (s *Server) handleUIItems(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	q := r.URL.Query().Get("q")
	tagFilter := tag.Normalize(r.URL.Query().Get("tag"))
	sort := defaultSort(r.URL.Query().Get("sort"))
	page := parseInt(r.URL.Query().Get("page"), 1)
	perPage := perPageValue(r.URL.Query().Get("per_page"))

	items, pag, err := s.store.ListItems(r.Context(), user.ID, page, perPage, q, tagFilter, sort)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	tags, _ := s.store.ListTagsWithCount(r.Context(), user.ID)

	data := map[string]interface{}{
		"Title":          "Items",
		"User":           user,
		"Items":          items,
		"Tags":           tags,
		"Page":           pag.Page,
		"PerPage":        pag.PerPage,
		"TotalPages":     max(1, (pag.Total+pag.PerPage-1)/pag.PerPage),
		"Query":          q,
		"Sort":           defaultSort(sort),
		"PerPageOptions": []int{10, 20, 30, 40, 50},
		"PrevURL":        pageURL(r.URL, pag.Page-1),
		"NextURL":        pageURL(r.URL, pag.Page+1),
		"CSRFToken":      s.csrfFromContext(r.Context()),
	}

	if err := s.renderer.Render(w, "items", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (s *Server) handleUIItem(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id := chi.URLParam(r, "id")
	item, err := s.store.GetItemDetail(r.Context(), user.ID, id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	data := map[string]interface{}{
		"Title":     "Item",
		"User":      user,
		"Item":      item,
		"CSRFToken": s.csrfFromContext(r.Context()),
	}
	if err := s.renderer.Render(w, "detail", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := s.checkCSRF(r); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "csrf"})
			return
		}
		user, ok := s.authenticate(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		ctx := auth.ContextWithUser(r.Context(), user)
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) requireWeb(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, user, ok := s.webSession(r)
		if !ok {
			http.Redirect(w, r, "/v1/auth/google/login", http.StatusFound)
			return
		}
		ctx := auth.ContextWithUser(r.Context(), auth.User{
			ID:        user.ID,
			GoogleSub: user.GoogleSub,
			Email:     user.Email,
			Name:      user.Name,
			AvatarURL: user.AvatarURL,
		})
		ctx = context.WithValue(ctx, csrfKey, sess.CSRFToken)
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) authenticate(r *http.Request) (auth.User, bool) {
	// Prefer Authorization for API
	if authz := r.Header.Get("Authorization"); strings.HasPrefix(authz, "Bearer ") {
		token := strings.TrimPrefix(authz, "Bearer ")
		userID, err := auth.ParseJWT(s.cfg.JWTSecret, token)
		if err != nil {
			return auth.User{}, false
		}
		usr, err := s.store.GetUserByID(r.Context(), userID)
		if err != nil {
			return auth.User{}, false
		}
		return auth.User{ID: usr.ID, GoogleSub: usr.GoogleSub, Email: usr.Email, Name: usr.Name, AvatarURL: usr.AvatarURL}, true
	}
	// Fallback to web session
	_, user, ok := s.webSession(r)
	if ok {
		return auth.User{ID: user.ID, GoogleSub: user.GoogleSub, Email: user.Email, Name: user.Name, AvatarURL: user.AvatarURL}, true
	}
	return auth.User{}, false
}

func (s *Server) webSession(r *http.Request) (store.Session, store.User, bool) {
	cookie, err := r.Cookie("altpocket_session")
	if err != nil || cookie.Value == "" {
		return store.Session{}, store.User{}, false
	}
	sess, err := s.store.GetSession(r.Context(), cookie.Value)
	if err != nil {
		return store.Session{}, store.User{}, false
	}
	user, err := s.store.GetUserByID(r.Context(), sess.UserID)
	if err != nil {
		return store.Session{}, store.User{}, false
	}
	return sess, user, true
}

func (s *Server) csrfFromContext(ctx context.Context) string {
	v := ctx.Value(csrfKey)
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func (s *Server) requestID(ctx context.Context) string {
	v := ctx.Value(requestIDKey)
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-CSRF-Token")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) checkCSRF(r *http.Request) error {
	if r.Method == http.MethodGet || r.Method == http.MethodOptions {
		return nil
	}
	if !strings.HasPrefix(r.URL.Path, "/v1/") {
		return nil
	}
	// Only enforce for session-based web requests
	if r.Header.Get("Authorization") != "" {
		return nil
	}
	cookie, err := r.Cookie("altpocket_session")
	if err != nil {
		return errors.New("missing session")
	}
	sess, err := s.store.GetSession(r.Context(), cookie.Value)
	if err != nil {
		return errors.New("missing session")
	}
	if r.Header.Get("X-CSRF-Token") != sess.CSRFToken {
		return errors.New("csrf")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func parseInt(v string, fallback int) int {
	if v == "" {
		return fallback
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return i
}

func perPageValue(v string) int {
	allowed := map[int]struct{}{10: {}, 20: {}, 30: {}, 40: {}, 50: {}}
	parsed := parseInt(v, 30)
	if _, ok := allowed[parsed]; !ok {
		return 30
	}
	return parsed
}

func defaultSort(v string) string {
	if v == "relevance" {
		return v
	}
	return "newest"
}

func pageURL(u *url.URL, page int) string {
	if page < 1 {
		page = 1
	}
	q := u.Query()
	q.Set("page", strconv.Itoa(page))
	u2 := *u
	u2.RawQuery = q.Encode()
	return u2.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
