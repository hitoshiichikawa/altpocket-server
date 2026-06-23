package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"altpocket/internal/auth"
	"altpocket/internal/config"
	"altpocket/internal/mcpserver"
	"altpocket/internal/ratelimit"
	"altpocket/internal/store"
	"altpocket/internal/tag"
	"altpocket/internal/ui"
	"altpocket/internal/urlnorm"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/idtoken"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
	"log/slog"

	"github.com/jackc/pgx/v5"
)

type Server struct {
	cfg               config.Config
	store             *store.Store
	limiter           *ratelimit.Limiter
	logger            *slog.Logger
	renderer          *ui.Renderer
	oauthCfg          *oauth2.Config
	sheetsOAuthCfg    *oauth2.Config
	randomStringFn    func(int) (string, error)
	oauthExchangeFn   func(context.Context, string) (*oauth2.Token, error)
	idTokenValidateFn func(context.Context, string, string) (*idtoken.Payload, error)
	// readyPingerFn overrides the DB ping used by /readyz. It exists
	// for tests so success / failure / timeout branches of the
	// readiness probe can be exercised without a live PostgreSQL.
	// When nil, /readyz falls back to s.store.DB (*pgxpool.Pool).
	readyPingerFn func(context.Context) error
}

var errInvalidURL = errors.New("invalid_url")

const maxCapturedContentPayloadBytes = 256 * 1024
const googleSheetsScope = "https://www.googleapis.com/auth/spreadsheets"
const googleDriveFileScope = "https://www.googleapis.com/auth/drive.file"
const quickAddContentPreviewRuneLimit = 200

func New(cfg config.Config, st *store.Store, limiter *ratelimit.Limiter, log *slog.Logger, renderer *ui.Renderer) *Server {
	oauthCfg := &oauth2.Config{
		ClientID:     cfg.GoogleWebClientID,
		ClientSecret: cfg.GoogleClientSecret,
		RedirectURL:  strings.TrimRight(cfg.PublicBaseURL, "/") + "/v1/auth/google/callback",
		Scopes:       []string{"openid", "email", "profile"},
		Endpoint:     google.Endpoint,
	}
	sheetsOAuthCfg := &oauth2.Config{
		ClientID:     cfg.GoogleWebClientID,
		ClientSecret: cfg.GoogleClientSecret,
		RedirectURL:  strings.TrimRight(cfg.PublicBaseURL, "/") + "/ui/settings/google/callback",
		Scopes:       []string{googleSheetsScope, googleDriveFileScope},
		Endpoint:     google.Endpoint,
	}

	return &Server{
		cfg:            cfg,
		store:          st,
		limiter:        limiter,
		logger:         log,
		renderer:       renderer,
		oauthCfg:       oauthCfg,
		sheetsOAuthCfg: sheetsOAuthCfg,
		randomStringFn: auth.RandomString,
		oauthExchangeFn: func(ctx context.Context, code string) (*oauth2.Token, error) {
			return oauthCfg.Exchange(ctx, code)
		},
		idTokenValidateFn: idtoken.Validate,
	}
}

func (s *Server) randomString(n int) (string, error) {
	if s.randomStringFn != nil {
		return s.randomStringFn(n)
	}
	return auth.RandomString(n)
}

func (s *Server) oauthExchange(ctx context.Context, code string) (*oauth2.Token, error) {
	if s.oauthExchangeFn != nil {
		return s.oauthExchangeFn(ctx, code)
	}
	return s.oauthCfg.Exchange(ctx, code)
}

func (s *Server) validateIDToken(ctx context.Context, token, audience string) (*idtoken.Payload, error) {
	if s.idTokenValidateFn != nil {
		return s.idTokenValidateFn(ctx, token, audience)
	}
	return idtoken.Validate(ctx, token, audience)
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(RequestID)
	r.Use(AccessLog(s.logger))

	r.Get("/", s.handleHome)
	r.Get("/register", s.handleRegister)
	r.Get("/healthz", s.handleHealth)
	r.Get("/readyz", s.handleReady)
	r.Get("/manifest.webmanifest", s.handleWebManifest)
	r.Get("/sw.js", s.handleServiceWorker)

	r.Route("/v1", func(r chi.Router) {
		r.Use(s.cors)
		r.Get("/auth/google/login", s.handleGoogleLogin)
		r.Get("/auth/google/callback", s.handleGoogleCallback)
		r.Post("/auth/extension/exchange", s.handleExtensionExchange)
		r.Post("/auth/extension/refresh", s.handleExtensionRefresh)
		r.Post("/auth/extension/logout", s.handleExtensionLogout)
		r.Get("/tags", s.requireAuth(s.handleTags))

		r.Route("/items", func(r chi.Router) {
			r.Get("/", s.requireAuth(s.handleListItems))
			r.Post("/", s.requireAuth(s.handleCreateItem))
			r.Get("/{id}", s.requireAuth(s.handleGetItem))
			r.Patch("/{id}", s.requireAuth(s.handlePatchItem))
			r.Put("/{id}/tags", s.requireAuth(s.handleUpdateItemTags))
			r.Post("/{id}/capture", s.requireAuth(s.handleCaptureItemContent))
			r.Delete("/{id}", s.requireAuth(s.handleDeleteItem))
			r.Post("/{id}/refetch", s.requireAuth(s.handleRefetchItem))
		})
	})

	r.Route("/ui", func(r chi.Router) {
		r.Get("/items", s.requireWeb(s.handleUIItems))
		r.Get("/items/{id}", s.requireWeb(s.handleUIItem))
		r.Get("/quick-add", s.requireWeb(s.handleUIQuickAdd))
		r.Post("/quick-add", s.requireWeb(s.handleUIQuickAddSubmit))
		r.Post("/quick-add/share-target", s.requireWeb(s.handleUIQuickAddShareTarget))
		r.Get("/settings", s.requireWeb(s.handleUISettings))
		r.Post("/settings/google/connect", s.requireWeb(s.handleUISettingsGoogleConnect))
		r.Get("/settings/google/callback", s.requireWeb(s.handleUISettingsGoogleCallback))
		r.Post("/settings/google/disconnect", s.requireWeb(s.handleUISettingsGoogleDisconnect))
		r.Post("/settings/google/export", s.requireWeb(s.handleUISettingsGoogleExport))
		r.Post("/settings/mcp/keys", s.requireWeb(s.handleMCPKeyGenerate))
		r.Post("/settings/mcp/keys/{id}/revoke", s.requireWeb(s.handleMCPKeyRevoke))
	})

	// MCP endpoint - always mounted, auth middleware handles access control
	mcpHandler := s.mcpHTTPHandler()
	r.Route("/mcp", func(r chi.Router) {
		r.Use(mcpserver.NewAuthMiddleware(s.store, s.logger))
		r.Handle("/*", mcpHandler)
		r.Handle("/", mcpHandler)
	})

	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	return r
}

func (s *Server) mcpHTTPHandler() http.Handler {
	// Streamable HTTP handler. A fresh per-user MCP server is constructed for
	// each request via getServer; the userID comes from the auth middleware.
	return mcpsdk.NewStreamableHTTPHandler(
		func(r *http.Request) *mcpsdk.Server {
			userID := mcpserver.UserIDFromContext(r.Context())
			if userID == "" {
				return nil
			}
			return mcpserver.New(s.store, userID)
		},
		nil,
	)
}

func (s *Server) handleWebManifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, "static/manifest.webmanifest")
}

func (s *Server) handleServiceWorker(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, "static/sw.js")
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	data := map[string]interface{}{
		"Title": "altpocket",
	}
	if _, user, ok := s.webSession(r); ok {
		data["User"] = user
	}
	if err := s.renderer.Render(w, "home", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.webSession(r); ok {
		http.Redirect(w, r, "/ui/items", http.StatusFound)
		return
	}
	data := map[string]interface{}{
		"Title": "アカウント登録",
	}
	if err := s.renderer.Render(w, "register", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (s *Server) handleGoogleLogin(w http.ResponseWriter, r *http.Request) {
	state, err := s.randomString(16)
	if err != nil {
		s.logger.Error("auth.google.login.state_generate_failed",
			slog.String("request_id", s.requestID(r.Context())),
			slog.String("error", err.Error()))
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
	requestID := s.requestID(r.Context())
	cookie, err := r.Cookie("oauth_state")
	stateParam := r.URL.Query().Get("state")
	cookieValue := ""
	if err == nil {
		cookieValue = cookie.Value
	}
	if cookieValue == "" || cookieValue != stateParam {
		s.logger.Warn("auth.google.callback.invalid_state",
			slog.String("request_id", requestID),
			slog.String("host", r.Host),
			slog.Bool("has_cookie_state", cookieValue != ""),
			slog.Bool("has_state_param", stateParam != ""))
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		s.logger.Warn("auth.google.callback.missing_code",
			slog.String("request_id", requestID),
			slog.String("host", r.Host),
			slog.String("oauth_error", r.URL.Query().Get("error")))
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	token, err := s.oauthExchange(r.Context(), code)
	if err != nil {
		s.logger.Warn("auth.google.callback.exchange_failed",
			slog.String("request_id", requestID),
			slog.String("host", r.Host))
		http.Error(w, "exchange failed", http.StatusBadRequest)
		return
	}
	idToken, ok := token.Extra("id_token").(string)
	if !ok || idToken == "" {
		s.logger.Warn("auth.google.callback.missing_id_token",
			slog.String("request_id", requestID),
			slog.String("host", r.Host))
		http.Error(w, "missing id_token", http.StatusBadRequest)
		return
	}
	payload, err := s.validateIDToken(r.Context(), idToken, s.cfg.GoogleWebClientID)
	if err != nil {
		s.logger.Warn("auth.google.callback.invalid_id_token",
			slog.String("request_id", requestID),
			slog.String("host", r.Host))
		http.Error(w, "invalid id_token", http.StatusUnauthorized)
		return
	}
	if payload.Subject == "" {
		s.logger.Warn("auth.google.callback.missing_subject",
			slog.String("request_id", requestID),
			slog.String("host", r.Host))
		http.Error(w, "invalid id_token", http.StatusUnauthorized)
		return
	}

	sub := payload.Subject
	email, _ := payload.Claims["email"].(string)
	name, _ := payload.Claims["name"].(string)
	avatar, _ := payload.Claims["picture"].(string)

	user, err := s.store.UpsertUser(r.Context(), sub, email, name, avatar)
	if err != nil {
		s.logger.Error("auth.google.callback.user_upsert_failed",
			slog.String("request_id", requestID),
			slog.String("error", err.Error()))
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	csrf, err := s.randomString(24)
	if err != nil {
		s.logger.Error("auth.google.callback.csrf_generate_failed",
			slog.String("request_id", requestID),
			slog.String("error", err.Error()))
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	sess, err := s.store.CreateSession(r.Context(), user.ID, csrf, config.SessionTTL())
	if err != nil {
		s.logger.Error("auth.google.callback.session_create_failed",
			slog.String("request_id", requestID),
			slog.String("error", err.Error()))
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
	payload, err := s.validateIDToken(r.Context(), req.IDToken, s.cfg.GoogleExtClientID)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
		return
	}
	if payload.Subject == "" {
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

	token, exp, err := auth.IssueJWT(s.cfg.JWTSecret, user.ID, config.ExtensionJWTTTL())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token_error"})
		return
	}

	// Issue a refresh token (opaque random string, stored hashed in DB).
	rawRefresh, err := s.randomString(32)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token_error"})
		return
	}
	refreshHash := hashToken(rawRefresh)
	_, err = s.store.CreateExtensionRefreshToken(r.Context(), user.ID, refreshHash, config.ExtensionRefreshTokenTTL())
	if err != nil {
		s.logger.Error("auth.extension.exchange.refresh_token_create_failed",
			slog.String("error", err.Error()))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token_error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token":         token,
		"expires_in":    exp - time.Now().Unix(),
		"refresh_token": rawRefresh,
	})
}

func (s *Server) handleExtensionRefresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RefreshToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}

	refreshHash := hashToken(req.RefreshToken)
	rt, err := s.store.GetExtensionRefreshToken(r.Context(), refreshHash)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
		return
	}

	// Issue a new short-lived JWT.
	user, err := s.store.GetUserByID(r.Context(), rt.UserID)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
		return
	}

	token, exp, err := auth.IssueJWT(s.cfg.JWTSecret, user.ID, config.ExtensionJWTTTL())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token_error"})
		return
	}

	// Slide the refresh token expiration forward.
	if err := s.store.TouchExtensionRefreshToken(r.Context(), rt.ID, config.ExtensionRefreshTokenTTL()); err != nil {
		s.logger.Error("auth.extension.refresh.touch_failed",
			slog.String("error", err.Error()))
		// Non-fatal: the new JWT is already issued, so continue.
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token":      token,
		"expires_in": exp - time.Now().Unix(),
	})
}

func (s *Server) handleExtensionLogout(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RefreshToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}

	refreshHash := hashToken(req.RefreshToken)
	_ = s.store.DeleteExtensionRefreshToken(r.Context(), refreshHash)
	w.WriteHeader(http.StatusNoContent)
}

// hashToken returns the hex-encoded SHA-256 hash of a raw token string.
func hashToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
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
		URL     string   `json:"url"`
		Tags    []string `json:"tags"`
		Title   string   `json:"title"`
		Excerpt string   `json:"excerpt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}

	title := truncateUTF8(strings.TrimSpace(req.Title), 500)
	excerpt := truncateUTF8(strings.TrimSpace(req.Excerpt), 200)

	itemID, created, err := s.createItem(r.Context(), user.ID, req.URL, req.Tags, title, excerpt)
	if err != nil {
		if errors.Is(err, errInvalidURL) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_url"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db_error"})
		return
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
	tagFilters := parseTagFilters(r.URL.Query())
	sort := defaultSort(r.URL.Query().Get("sort"))
	page := parseInt(r.URL.Query().Get("page"), 1)
	perPage := perPageValue(r.URL.Query().Get("per_page"))

	items, pag, err := s.store.ListItems(r.Context(), user.ID, page, perPage, q, tagFilters, sort)
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

func (s *Server) handleCaptureItemContent(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxCapturedContentPayloadBytes)
	var req struct {
		Title       string `json:"title"`
		ContentFull string `json:"content_full"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}

	contentFull := truncateUTF8(strings.TrimSpace(req.ContentFull), s.cfg.ContentFullLimit)
	if contentFull == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}

	searchText := normalizeWhitespace(contentFull)
	excerpt := truncateUTF8(searchText, 200)
	contentSearch := truncateUTF8(searchText, s.cfg.ContentSearchLimit)
	itemID := chi.URLParam(r, "id")

	if err := s.store.SeedCapturedContent(
		r.Context(),
		user.ID,
		itemID,
		strings.TrimSpace(req.Title),
		excerpt,
		contentFull,
		contentSearch,
		len([]byte(contentFull)),
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db_error"})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUpdateItemTags(w http.ResponseWriter, r *http.Request) {
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
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}

	itemID := chi.URLParam(r, "id")
	tags, err := s.store.ReplaceItemTags(r.Context(), user.ID, itemID, normalizeTagInputs(req.Tags))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db_error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"tags": tags})
}

func (s *Server) handlePatchItem(w http.ResponseWriter, r *http.Request) {
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
		Title *string  `json:"title"`
		Tags  *[]string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}

	// Trim and validate title if provided
	if req.Title != nil {
		trimmed := strings.TrimSpace(*req.Title)
		if trimmed == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
			return
		}
		req.Title = &trimmed
	}

	// Normalize tags if provided. We pair the original display name with the
	// canonical normalized key (TagInput) so the chip column can render the
	// user-entered casing (Issue #115 / AC 1.3).
	var tagInputs *[]store.TagInput
	if req.Tags != nil {
		ti := normalizeTagInputs(*req.Tags)
		tagInputs = &ti
	}

	itemID := chi.URLParam(r, "id")
	title, tags, err := s.store.PatchItem(r.Context(), user.ID, itemID, req.Title, tagInputs)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db_error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"title": title, "tags": tags})
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
	tagFilters := parseTagFilters(r.URL.Query())
	sort := defaultSort(r.URL.Query().Get("sort"))
	page := parseInt(r.URL.Query().Get("page"), 1)
	perPage := perPageValue(r.URL.Query().Get("per_page"))

	items, pag, err := s.store.ListItems(r.Context(), user.ID, page, perPage, q, tagFilters, sort)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	fragmentOnly := wantsItemsFragment(r)

	// For full-page renders we also need the Tags facet for the sidebar. The
	// fragment path skips this query because the sidebar is not re-rendered
	// on debounce-driven swaps and the user's selected tag chips are kept
	// intact by client-side JS.
	//
	// Both paths must additionally resolve the active filters' user-entered
	// display names directly from the tags table when tag filters are present.
	// The facet aggregate (ListTagsWithCountFiltered) only surfaces tags that
	// appear in the *filtered* result set, so a tag AND-condition that yields
	// zero items returns an empty facet. Without the direct lookup the active
	// filter chips would degrade to the normalized lowercase form for such
	// zero-result URLs — violating AC 1.3 (chips show the original display
	// name) and AC 4.5 (direct URL open matches the query). The facet is kept
	// as the higher-priority source so it still wins for its canonical casing
	// on non-empty results; the direct lookup only fills the gaps
	// (Issue #115 round-3 review).
	var tags any
	var tagsForLookup []store.Tag
	if !fragmentOnly {
		t, _ := s.store.ListTagsWithCountFiltered(r.Context(), user.ID, q, tagFilters)
		tags = t
		tagsForLookup = t
	}
	if len(tagFilters) > 0 {
		named, _ := s.store.TagsByNormalizedNames(r.Context(), user.ID, tagFilters)
		// Facet entries (when present) keep priority; the direct lookup is
		// appended so buildActiveTagFilters' earlier-source-wins dedup uses it
		// only for filters the facet did not surface (e.g. zero-result tags).
		tagsForLookup = mergeTagDisplaySources(tagsForLookup, named)
	}

	// Active filter chips shown above the item list (Issue #115). The display
	// name is resolved from the Tags facet (full-page) merged with the direct
	// tag lookup (both paths when filters are present) / the items' own Tags
	// (fallback) so the chip shows the user-entered name rather than the
	// normalized lowercase form even for zero-result filters. Tags that cannot
	// be resolved fall back to their normalized name as a last resort.
	activeTagFilters := buildActiveTagFilters(tagFilters, tagsForLookup, items, r.URL)

	data := map[string]interface{}{
		"Title":            "記事一覧",
		"User":             user,
		"ActiveNav":        "items",
		"Items":            items,
		"Tags":             tags,
		"SelectedTags":     selectedTagSet(tagFilters),
		"ActiveTagFilters": activeTagFilters,
		"ClearAllTagsURL":  buildClearAllTagsURL(r.URL),
		"Page":             pag.Page,
		"PerPage":          pag.PerPage,
		"Total":            pag.Total,
		"TotalPages":       max(1, (pag.Total+pag.PerPage-1)/pag.PerPage),
		"Query":            q,
		"Sort":             defaultSort(sort),
		"PerPageOptions":   []int{10, 20, 30, 40, 50},
		"PrevURL":          pageURL(r.URL, pag.Page-1),
		"NextURL":          pageURL(r.URL, pag.Page+1),
		"CSRFToken":        s.csrfFromContext(r.Context()),
		"QuickAddNotice":   quickAddNotice(r.URL.Query().Get("quick_add")),
	}

	if fragmentOnly {
		if err := s.renderer.RenderFragment(w, "items_list", data); err != nil {
			http.Error(w, "render error", http.StatusInternalServerError)
		}
		return
	}

	if err := s.renderer.Render(w, "items", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

// wantsItemsFragment returns true when the caller asked for the items list as
// an HTML fragment instead of the full page. The contract is the request
// header `X-Requested-With: ItemsFragment`, which is sent by the search
// debounce / URL sync flow on /ui/items (Issue #114).
//
// The match is case-insensitive on the value; absence of the header (or any
// other value, including an empty string) means a full-page render.
func wantsItemsFragment(r *http.Request) bool {
	if r == nil {
		return false
	}
	return strings.EqualFold(r.Header.Get("X-Requested-With"), "ItemsFragment")
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
	pageTitle := item.Title
	if pageTitle == "" {
		pageTitle = "(無題)"
	}
	data := map[string]interface{}{
		"Title":     pageTitle,
		"User":      user,
		"ActiveNav": "items",
		"Item":      item,
		"CSRFToken": s.csrfFromContext(r.Context()),
	}
	if err := s.renderer.Render(w, "detail", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (s *Server) handleUISettings(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := s.store.GetGoogleSheetsConnection(r.Context(), user.ID)
	connected := true
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			connected = false
		case errors.Is(err, store.ErrRefreshTokenDecryptFailed):
			// Treat decryption failures (wrong key, tampered ciphertext,
			// legacy plaintext row) the same as "not connected" so the
			// user is nudged to re-authorize. We log a structured event
			// for operators (no key, ciphertext, or plaintext) so the
			// failure rate is observable. (Req 2.3, 2.4, NFR 3.2)
			s.logger.Warn("settings.google_sheets.decrypt_failed",
				slog.String("request_id", s.requestID(r.Context())),
				slog.String("user_id", user.ID))
			connected = false
		default:
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
	}

	noticeMsg, noticeClass := settingsNotice(r.URL.Query().Get("status"))
	if mcpMsg, mcpClass := settingsMCPNotice(r.URL.Query().Get("status")); mcpMsg != "" {
		noticeMsg = mcpMsg
		noticeClass = mcpClass
	}
	if custom := strings.TrimSpace(r.URL.Query().Get("message")); custom != "" {
		noticeMsg = custom
		if noticeClass == "" {
			noticeClass = "notice"
		}
	}
	sheetURL := strings.TrimSpace(r.URL.Query().Get("sheet_url"))
	if sheetURL == "" {
		sheetURL = googleSheetURL(conn.SpreadsheetID)
	}

	mcpKeys, _ := s.store.ListMCPAPIKeys(r.Context(), user.ID)
	newMCPKey := s.consumeMCPNewKeyCookie(w, r)

	data := map[string]interface{}{
		"Title":                 "設定",
		"User":                  user,
		"ActiveNav":             "settings",
		"CSRFToken":             s.csrfFromContext(r.Context()),
		"GoogleSheetsConnected": connected,
		"GoogleSheetsSheetURL":  googleSheetURL(conn.SpreadsheetID),
		"SheetURL":              sheetURL,
		"NoticeMessage":         noticeMsg,
		"NoticeClass":           noticeClass,
		"MCPAPIKeys":            mcpKeys,
		"NewMCPKey":             newMCPKey,
		"MCPEndpointURL":        s.mcpEndpointURL(),
	}
	if err := s.renderer.Render(w, "settings", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (s *Server) handleUISettingsGoogleConnect(w http.ResponseWriter, r *http.Request) {
	csrfExpected := s.csrfFromContext(r.Context())
	csrfProvided := r.PostFormValue("csrf_token")
	if csrfExpected == "" || csrfProvided == "" || csrfExpected != csrfProvided {
		http.Redirect(w, r, "/ui/settings?status=csrf_error", http.StatusFound)
		return
	}

	state, err := s.randomString(16)
	if err != nil {
		http.Redirect(w, r, "/ui/settings?status=google_connect_failed", http.StatusFound)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "google_sheets_oauth_state",
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   strings.HasPrefix(s.cfg.PublicBaseURL, "https://"),
		MaxAge:   300,
	})

	authURL := s.sheetsOAuthCfg.AuthCodeURL(
		state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"),
		oauth2.SetAuthURLParam("include_granted_scopes", "true"),
	)
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (s *Server) handleUISettingsGoogleCallback(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/ui/settings?status=unauthorized", http.StatusFound)
		return
	}

	stateCookie, err := r.Cookie("google_sheets_oauth_state")
	stateParam := r.URL.Query().Get("state")
	cookieValue := ""
	if err == nil {
		cookieValue = stateCookie.Value
	}
	if cookieValue == "" || cookieValue != stateParam {
		http.Redirect(w, r, "/ui/settings?status=google_connect_failed&message=Invalid+oauth+state", http.StatusFound)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "google_sheets_oauth_state", Value: "", Path: "/", MaxAge: -1})

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Redirect(w, r, "/ui/settings?status=google_connect_failed&message=Missing+authorization+code", http.StatusFound)
		return
	}

	token, err := s.sheetsOAuthCfg.Exchange(r.Context(), code)
	if err != nil {
		http.Redirect(w, r, "/ui/settings?status=google_connect_failed&message=OAuth+exchange+failed", http.StatusFound)
		return
	}

	refreshToken := strings.TrimSpace(token.RefreshToken)
	if refreshToken == "" {
		existing, getErr := s.store.GetGoogleSheetsConnection(r.Context(), user.ID)
		if getErr == nil {
			refreshToken = existing.RefreshToken
		}
	}
	if refreshToken == "" {
		http.Redirect(w, r, "/ui/settings?status=google_connect_failed&message=Refresh+token+not+issued", http.StatusFound)
		return
	}

	if err := s.store.UpsertGoogleSheetsConnection(r.Context(), user.ID, refreshToken); err != nil {
		http.Redirect(w, r, "/ui/settings?status=google_connect_failed", http.StatusFound)
		return
	}

	http.Redirect(w, r, "/ui/settings?status=google_connected", http.StatusFound)
}

func (s *Server) handleUISettingsGoogleDisconnect(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/ui/settings?status=unauthorized", http.StatusFound)
		return
	}

	csrfExpected := s.csrfFromContext(r.Context())
	csrfProvided := r.PostFormValue("csrf_token")
	if csrfExpected == "" || csrfProvided == "" || csrfExpected != csrfProvided {
		http.Redirect(w, r, "/ui/settings?status=csrf_error", http.StatusFound)
		return
	}

	if err := s.store.DeleteGoogleSheetsConnection(r.Context(), user.ID); err != nil {
		http.Redirect(w, r, "/ui/settings?status=google_disconnect_failed", http.StatusFound)
		return
	}

	http.Redirect(w, r, "/ui/settings?status=google_disconnected", http.StatusFound)
}

func (s *Server) handleUISettingsGoogleExport(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/ui/settings?status=unauthorized", http.StatusFound)
		return
	}

	csrfExpected := s.csrfFromContext(r.Context())
	csrfProvided := r.PostFormValue("csrf_token")
	if csrfExpected == "" || csrfProvided == "" || csrfExpected != csrfProvided {
		http.Redirect(w, r, "/ui/settings?status=csrf_error", http.StatusFound)
		return
	}

	conn, err := s.store.GetGoogleSheetsConnection(r.Context(), user.ID)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			http.Redirect(w, r, "/ui/settings?status=google_not_connected", http.StatusFound)
			return
		case errors.Is(err, store.ErrRefreshTokenDecryptFailed):
			// Same UX as "not connected": the existing
			// google_not_connected notice tells the user to re-authorize.
			// We log a structured event (no key, ciphertext, plaintext,
			// or OAuth response) so operators can monitor the failure
			// rate without leaking secrets.
			// (Req 1.5, 2.2, 2.3, 2.4, NFR 1.2, NFR 1.3, NFR 2.1, NFR 3.2)
			s.logger.Warn("settings.google_sheets.decrypt_failed",
				slog.String("request_id", s.requestID(r.Context())),
				slog.String("user_id", user.ID))
			http.Redirect(w, r, "/ui/settings?status=google_not_connected", http.StatusFound)
			return
		default:
			http.Redirect(w, r, "/ui/settings?status=export_failed", http.StatusFound)
			return
		}
	}

	// conn.RefreshToken is plaintext at this point. It MUST stay scoped
	// to this request: it is only passed into oauth2.Token via
	// exportItemsToGoogleSheets below and never logged, persisted, or
	// captured by a goroutine that outlives the request (Req 2.2,
	// NFR 1.3).
	sheetURL, err := s.exportItemsToGoogleSheets(r.Context(), user.ID, conn)
	if err != nil {
		s.logger.Warn("settings.google_sheets.export_failed",
			slog.String("request_id", s.requestID(r.Context())),
			slog.String("user_id", user.ID),
			slog.String("error", err.Error()))
		http.Redirect(w, r, "/ui/settings?status=export_failed", http.StatusFound)
		return
	}

	target := "/ui/settings?status=export_success"
	if sheetURL != "" {
		target += "&sheet_url=" + url.QueryEscape(sheetURL)
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) handleUIQuickAdd(w http.ResponseWriter, r *http.Request) {
	urlValue := strings.TrimSpace(r.URL.Query().Get("url"))
	textValue := strings.TrimSpace(r.URL.Query().Get("text"))
	if urlValue == "" {
		urlValue = extractHTTPURLFromText(textValue)
	}
	contentPreview := sanitizeQuickAddContent(r.URL.Query().Get("content"))
	if contentPreview == "" {
		contentPreview = quickAddContentPreview(urlValue, textValue)
	}

	s.renderUIQuickAdd(
		w,
		r,
		http.StatusOK,
		urlValue,
		strings.TrimSpace(r.URL.Query().Get("title")),
		strings.TrimSpace(r.URL.Query().Get("tags")),
		contentPreview,
		"",
	)
}

func (s *Server) handleUIQuickAddShareTarget(w http.ResponseWriter, r *http.Request) {
	urlValue := strings.TrimSpace(r.PostFormValue("url"))
	titleValue := strings.TrimSpace(r.PostFormValue("title"))
	textValue := strings.TrimSpace(r.PostFormValue("text"))
	if urlValue == "" {
		urlValue = extractHTTPURLFromText(textValue)
	}

	q := url.Values{}
	if urlValue != "" {
		q.Set("url", urlValue)
	}
	if titleValue != "" {
		q.Set("title", titleValue)
	}
	contentPreview := quickAddContentPreview(urlValue, textValue)
	if contentPreview != "" {
		q.Set("content", contentPreview)
	}

	target := "/ui/quick-add"
	if encoded := q.Encode(); encoded != "" {
		target += "?" + encoded
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) handleUIQuickAddSubmit(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	urlValue := strings.TrimSpace(r.PostFormValue("url"))
	titleValue := strings.TrimSpace(r.PostFormValue("title"))
	tagsValue := strings.TrimSpace(r.PostFormValue("tags"))
	contentPreview := sanitizeQuickAddContentInput(r.PostFormValue("content"), s.cfg.ContentFullLimit)
	if contentPreview == "" {
		contentPreview = sanitizeQuickAddContent(r.PostFormValue("content_preview"))
	}

	csrfExpected := s.csrfFromContext(r.Context())
	csrfProvided := r.PostFormValue("csrf_token")
	if csrfExpected == "" || csrfProvided == "" || csrfProvided != csrfExpected {
		s.renderUIQuickAdd(w, r, http.StatusForbidden, urlValue, titleValue, tagsValue, contentPreview, "CSRF token mismatch.")
		return
	}
	if urlValue == "" {
		s.renderUIQuickAdd(w, r, http.StatusBadRequest, urlValue, titleValue, tagsValue, contentPreview, "URL is required.")
		return
	}
	if !s.limiter.Allow(user.ID) {
		s.renderUIQuickAdd(w, r, http.StatusTooManyRequests, urlValue, titleValue, tagsValue, contentPreview, "Too many requests. Please wait and retry.")
		return
	}

	title := truncateUTF8(titleValue, 500)
	excerpt := truncateUTF8(normalizeWhitespace(contentPreview), 200)
	itemID, created, err := s.createItem(r.Context(), user.ID, urlValue, parseTagInput(tagsValue), title, excerpt)
	if err != nil {
		if errors.Is(err, errInvalidURL) {
			s.renderUIQuickAdd(w, r, http.StatusBadRequest, urlValue, titleValue, tagsValue, contentPreview, "Invalid URL.")
			return
		}
		s.renderUIQuickAdd(w, r, http.StatusInternalServerError, urlValue, titleValue, tagsValue, contentPreview, "Failed to save item.")
		return
	}

	s.seedQuickAddContent(r.Context(), user.ID, itemID, titleValue, contentPreview)

	if created {
		http.Redirect(w, r, "/ui/items?quick_add=created", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/ui/items?quick_add=exists", http.StatusFound)
}

func (s *Server) renderUIQuickAdd(w http.ResponseWriter, r *http.Request, status int, urlValue, titleValue, tagsValue, contentPreview, errMsg string) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	data := map[string]interface{}{
		"Title":          "クイック追加",
		"User":           user,
		"ActiveNav":      "quick-add",
		"CSRFToken":      s.csrfFromContext(r.Context()),
		"URL":            urlValue,
		"SourceTitle":    titleValue,
		"Tags":           tagsValue,
		"ContentPreview": contentPreview,
		"Error":          errMsg,
	}
	if status != http.StatusOK {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
	}
	if err := s.renderer.Render(w, "quick_add", data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (s *Server) seedQuickAddContent(ctx context.Context, userID, itemID, titleValue, contentPreview string) {
	if s.store == nil || contentPreview == "" {
		return
	}

	contentFull := truncateUTF8(contentPreview, s.cfg.ContentFullLimit)
	searchText := normalizeWhitespace(contentFull)
	excerpt := truncateUTF8(searchText, 200)
	contentSearch := truncateUTF8(searchText, s.cfg.ContentSearchLimit)

	if err := s.store.SeedCapturedContent(
		ctx,
		userID,
		itemID,
		titleValue,
		excerpt,
		contentFull,
		contentSearch,
		len([]byte(contentFull)),
	); err != nil {
		s.logger.Warn("ui.quick_add.capture_failed",
			slog.String("request_id", s.requestID(ctx)),
			slog.String("item_id", itemID),
			slog.String("error", err.Error()))
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
	allowed := s.cfg.CORSAllowOrigins
	allowedSet := map[string]struct{}{}
	for _, o := range allowed {
		allowedSet[o] = struct{}{}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" {
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		_, allowedOrigin := allowedSet[origin]
		if !allowedOrigin && !requestOriginMatchesHost(r, origin) {
			w.WriteHeader(http.StatusForbidden)
			return
		}

		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Add("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-CSRF-Token")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requestOriginMatchesHost(r *http.Request, origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
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

func (s *Server) createItem(ctx context.Context, userID, rawURL string, rawTags []string, title, excerpt string) (string, bool, error) {
	canonicalURL, canonicalHash, err := urlnorm.Canonicalize(rawURL)
	if err != nil {
		return "", false, errInvalidURL
	}

	tagInputs := normalizeTagInputs(rawTags)
	itemID, created, err := s.store.CreateItem(ctx, userID, rawURL, canonicalURL, canonicalHash, tagInputs, title, excerpt)
	if err != nil {
		return "", false, err
	}

	if created {
		s.logger.Info("items.create", slog.String("item_id", itemID), slog.Bool("created", true), slog.String("request_id", s.requestID(ctx)))
		return itemID, true, nil
	}
	s.logger.Info("items.create", slog.String("item_id", itemID), slog.Bool("created", false), slog.String("request_id", s.requestID(ctx)))
	s.logger.Info("duplicate_noop", slog.String("item_id", itemID), slog.String("request_id", s.requestID(ctx)))
	return itemID, false, nil
}

func normalizeTagNames(rawTags []string) []string {
	normTags := make([]string, 0, len(rawTags))
	seen := map[string]struct{}{}
	for _, t := range rawTags {
		norm := tag.Normalize(t)
		if norm == "" {
			continue
		}
		if _, ok := seen[norm]; ok {
			continue
		}
		seen[norm] = struct{}{}
		normTags = append(normTags, norm)
	}
	return normTags
}

// normalizeTagInputs is the write-side counterpart of normalizeTagNames: it
// preserves the user-entered display Name alongside the canonical
// NormalizedName key, deduping by normalized key. The first occurrence of a
// given normalized form wins the display Name (Issue #115 / AC 1.3 — chip must
// show original "Go Lang" instead of normalized "go lang").
func normalizeTagInputs(rawTags []string) []store.TagInput {
	inputs := make([]store.TagInput, 0, len(rawTags))
	seen := map[string]struct{}{}
	for _, t := range rawTags {
		norm := tag.Normalize(t)
		if norm == "" {
			continue
		}
		if _, ok := seen[norm]; ok {
			continue
		}
		seen[norm] = struct{}{}
		inputs = append(inputs, store.TagInput{
			Name:           tag.DisplayName(t),
			NormalizedName: norm,
		})
	}
	return inputs
}

// parseTagInput splits a user-entered tag string (comma / semicolon / newline
// delimited) into raw tokens without normalizing. Display-name preservation is
// performed downstream by normalizeTagInputs so that the original casing
// reaches tags.name (Issue #115 / AC 1.3).
func parseTagInput(v string) []string {
	return strings.FieldsFunc(v, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	})
}

func parseTagFilters(q url.Values) []string {
	raw := make([]string, 0, len(q["tag"])+1)
	raw = append(raw, q["tag"]...)
	if csv := strings.TrimSpace(q.Get("tags")); csv != "" {
		raw = append(raw, strings.Split(csv, ",")...)
	}
	return normalizeTagNames(raw)
}

func selectedTagSet(tags []string) map[string]bool {
	selected := make(map[string]bool, len(tags))
	for _, t := range tags {
		selected[t] = true
	}
	return selected
}

// ActiveTagFilter is one chip shown above the items list (Issue #115).
//
//   - Name: original display name as entered by the user (or normalized form
//     if no source can resolve a display name)
//   - NormalizedName: canonical form used by the server filter and the URL
//   - RemoveURL: URL with this single tag removed from the filter set,
//     preserving every other query parameter (q, sort, per_page, page).
//     Used as the chip's `<a href>` so the SSR fallback works when JS is
//     disabled (NFR 2.1) and as the canonical target the JS path also
//     navigates to via history.pushState (Req 5.1 / 5.2).
type ActiveTagFilter struct {
	Name           string
	NormalizedName string
	RemoveURL      string
}

// mergeTagDisplaySources concatenates two Tag slices used for active-filter
// chip display-name resolution, with `primary` kept ahead of `secondary`.
//
// buildActiveTagFilters resolves display names with an earlier-source-wins
// dedup, so ordering here encodes priority: the filtered facet (primary) keeps
// its canonical user-entered casing for tags it surfaced, while the direct
// TagsByNormalizedNames lookup (secondary) fills in display names for filters
// the facet did not include — notably zero-result tag AND-conditions where the
// facet is empty (Issue #115). Either argument may be nil.
func mergeTagDisplaySources(primary, secondary []store.Tag) []store.Tag {
	if len(secondary) == 0 {
		return primary
	}
	if len(primary) == 0 {
		return secondary
	}
	merged := make([]store.Tag, 0, len(primary)+len(secondary))
	merged = append(merged, primary...)
	merged = append(merged, secondary...)
	return merged
}

// buildActiveTagFilters constructs the chip list for the active filter row.
//
// Display name resolution priority:
//  1. The Tags facet (full-page renders) — covers the common case where the
//     filtered result contains at least one matching tag and the sidebar
//     facet has been computed.
//  2. The items' own Tags (fragment renders skip the facet query for
//     performance) — covers the fragment path where the items returned for
//     the current page necessarily include each active filter tag.
//  3. The normalized name itself — fallback for zero-result filters where
//     neither source resolves the display name.
//
// The RemoveURL field is a fully-formed query string with only the target
// tag removed (and the legacy `?tags=` plural form fully dropped to keep the
// canonical `?tag=<normalized>` repetition shape per Req 5.1).
func buildActiveTagFilters(tagFilters []string, facetTags []store.Tag, items []store.ItemListRow, currentURL *url.URL) []ActiveTagFilter {
	if len(tagFilters) == 0 {
		return nil
	}
	// Build a normalized -> display name lookup. Earlier sources win so that
	// the facet (which carries the canonical user-entered casing for tags
	// surfaced through the sidebar) takes priority over the per-item Tags.
	display := make(map[string]string, len(tagFilters))
	for _, t := range facetTags {
		if _, ok := display[t.NormalizedName]; !ok && t.Name != "" {
			display[t.NormalizedName] = t.Name
		}
	}
	for _, it := range items {
		for _, t := range it.Tags {
			if _, ok := display[t.NormalizedName]; !ok && t.Name != "" {
				display[t.NormalizedName] = t.Name
			}
		}
	}

	out := make([]ActiveTagFilter, 0, len(tagFilters))
	for _, norm := range tagFilters {
		name := display[norm]
		if name == "" {
			name = norm
		}
		out = append(out, ActiveTagFilter{
			Name:           name,
			NormalizedName: norm,
			RemoveURL:      buildTagRemovedURL(currentURL, norm, tagFilters),
		})
	}
	return out
}

// buildTagRemovedURL returns a URL with the given normalized tag removed
// from the current filter set, preserving every other query parameter
// including `page` (Req 5.2). Both the canonical `?tag=` repetition and the
// legacy `?tags=` plural form are stripped and the remaining tags are
// re-emitted in the canonical form (Req 5.1). When the resulting set is
// empty no `tag` / `tags` parameter is written (Req 5.3).
func buildTagRemovedURL(currentURL *url.URL, removeNorm string, current []string) string {
	u := cloneURL(currentURL)
	q := u.Query()
	q.Del("tag")
	q.Del("tags")
	for _, t := range current {
		if t == removeNorm {
			continue
		}
		q.Add("tag", t)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// buildClearAllTagsURL returns a URL with every tag filter parameter
// removed (Req 3.6, 5.3). All other query parameters are preserved
// (Req 5.2), including `page`.
func buildClearAllTagsURL(currentURL *url.URL) string {
	u := cloneURL(currentURL)
	q := u.Query()
	q.Del("tag")
	q.Del("tags")
	u.RawQuery = q.Encode()
	return u.String()
}

// cloneURL returns a shallow copy of u that callers may mutate (RawQuery,
// query values) without affecting the caller's pointer.
func cloneURL(u *url.URL) *url.URL {
	if u == nil {
		return &url.URL{}
	}
	c := *u
	return &c
}

func normalizeWhitespace(v string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(v)), " ")
}

func truncateUTF8(v string, limit int) string {
	if limit <= 0 {
		return ""
	}
	b := []byte(v)
	if len(b) <= limit {
		return v
	}
	trunc := b[:limit]
	for len(trunc) > 0 && !utf8.Valid(trunc) {
		trunc = trunc[:len(trunc)-1]
	}
	return string(trunc)
}

func extractHTTPURLFromText(v string) string {
	if v == "" {
		return ""
	}
	for _, part := range strings.Fields(v) {
		candidate := strings.Trim(part, "\"'()[]{}<>,.;")
		u, err := url.Parse(candidate)
		if err != nil {
			continue
		}
		if (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
			return candidate
		}
	}
	return ""
}

func (s *Server) exportItemsToGoogleSheets(ctx context.Context, userID string, conn store.GoogleSheetsConnection) (string, error) {
	token := &oauth2.Token{RefreshToken: conn.RefreshToken}
	tokenSource := s.sheetsOAuthCfg.TokenSource(ctx, token)
	client := oauth2.NewClient(ctx, tokenSource)
	sheetsService, err := sheets.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", err
	}

	spreadsheetID := strings.TrimSpace(conn.SpreadsheetID)
	if spreadsheetID == "" {
		title := fmt.Sprintf("altpocket export %s", time.Now().Format("2006-01-02"))
		created, err := sheetsService.Spreadsheets.Create(&sheets.Spreadsheet{
			Properties: &sheets.SpreadsheetProperties{Title: title},
		}).Do()
		if err != nil {
			return "", err
		}
		spreadsheetID = created.SpreadsheetId
		if err := s.store.SetGoogleSheetsSpreadsheetID(ctx, userID, spreadsheetID); err != nil {
			return "", err
		}
	}

	items, err := s.store.ListItemsForExport(ctx, userID)
	if err != nil {
		return "", err
	}

	values := make([][]interface{}, 0, len(items)+1)
	values = append(values, []interface{}{
		"item_id",
		"url",
		"title",
		"excerpt",
		"tags",
		"fetch_status",
		"fetch_error",
		"created_at",
		"fetched_at",
	})
	for _, item := range items {
		fetchedAt := ""
		if item.FetchedAt != nil {
			fetchedAt = item.FetchedAt.UTC().Format(time.RFC3339)
		}
		values = append(values, []interface{}{
			item.ID,
			item.URL,
			item.Title,
			item.Excerpt,
			strings.Join(item.Tags, ","),
			item.FetchStatus,
			item.FetchError,
			item.CreatedAt.UTC().Format(time.RFC3339),
			fetchedAt,
		})
	}

	if _, err := sheetsService.Spreadsheets.Values.Clear(spreadsheetID, "A:Z", &sheets.ClearValuesRequest{}).Do(); err != nil {
		return "", err
	}
	if _, err := sheetsService.Spreadsheets.Values.Update(spreadsheetID, "A1", &sheets.ValueRange{Values: values}).ValueInputOption("RAW").Do(); err != nil {
		return "", err
	}
	return googleSheetURL(spreadsheetID), nil
}

func googleSheetURL(spreadsheetID string) string {
	if strings.TrimSpace(spreadsheetID) == "" {
		return ""
	}
	return "https://docs.google.com/spreadsheets/d/" + spreadsheetID + "/edit"
}

func settingsNotice(state string) (string, string) {
	switch state {
	case "google_connected":
		return "Google account connected.", "notice"
	case "google_disconnected":
		return "Google account disconnected.", "notice"
	case "export_success":
		return "Export completed.", "notice"
	case "google_not_connected":
		return "Connect Google before exporting.", "error"
	case "csrf_error":
		return "Invalid CSRF token. Please retry.", "error"
	case "google_connect_failed":
		return "Google connection failed.", "error"
	case "google_disconnect_failed":
		return "Failed to disconnect Google account.", "error"
	case "export_failed":
		return "Export failed.", "error"
	case "unauthorized":
		return "Unauthorized.", "error"
	default:
		return "", ""
	}
}

func quickAddContentPreview(urlValue, textValue string) string {
	candidate := normalizeWhitespace(textValue)
	if candidate == "" {
		return ""
	}
	if urlValue != "" {
		candidate = strings.ReplaceAll(candidate, urlValue, " ")
	}
	return sanitizeQuickAddContent(candidate)
}

func sanitizeQuickAddContent(v string) string {
	return truncateRunes(normalizeWhitespace(v), quickAddContentPreviewRuneLimit)
}

func sanitizeQuickAddContentInput(v string, limit int) string {
	return truncateUTF8(normalizeWhitespace(v), limit)
}

func truncateRunes(v string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(v)
	if len(runes) <= limit {
		return v
	}
	return string(runes[:limit])
}

func quickAddNotice(state string) string {
	switch state {
	case "created":
		return "Saved page."
	case "exists":
		return "This page is already saved."
	default:
		return ""
	}
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
