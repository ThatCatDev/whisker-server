// whisker-server: sync + boards backend for the Whisker whiteboard.
//
// One binary, three jobs:
//   - /sync/{board}    Yjs websocket relay (dumb: documents are opaque bytes)
//   - /api/boards      board registry REST API
//   - Supabase JWT verification in front of both
//
// Configuration (environment):
//
//	ADDR                listen address              (default :8787)
//	DATABASE_URL        Postgres DSN; empty = in-memory dev store
//	SUPABASE_JWT_SECRET Supabase project JWT secret (HS256)
//	AUTH_DISABLED=1     skip auth, single dev user  (development only)
//	OPEN_BOARDS=1       skip board ACL on /sync     (development only;
//	                    implied by AUTH_DISABLED)
//	CORS_ORIGIN         Access-Control-Allow-Origin (default *)
package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"

	"github.com/coder/websocket"

	"whisker-server/internal/api"
	"whisker-server/internal/auth"
	"whisker-server/internal/hub"
	"whisker-server/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	addr := envOr("ADDR", ":8787")
	authDisabled := os.Getenv("AUTH_DISABLED") == "1"
	openBoards := os.Getenv("OPEN_BOARDS") == "1" || authDisabled
	jwtSecret := os.Getenv("SUPABASE_JWT_SECRET")

	if authDisabled {
		log.Warn("AUTH_DISABLED=1 — every request runs as 'dev-user'")
	} else if jwtSecret == "" {
		log.Error("SUPABASE_JWT_SECRET is required unless AUTH_DISABLED=1")
		os.Exit(1)
	}

	var st store.Store
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		pg, err := store.NewPostgres(context.Background(), dsn)
		if err != nil {
			log.Error("postgres", "err", err)
			os.Exit(1)
		}
		st = pg
		log.Info("storage: postgres")
	} else {
		st = store.NewMemory()
		log.Warn("storage: in-memory (no DATABASE_URL) — nothing survives restarts")
	}
	defer st.Close()

	verifier := auth.New(jwtSecret, authDisabled)
	h := hub.New(st, log)

	mux := http.NewServeMux()
	api.New(st, verifier, log).Register(mux)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Reverse-proxy the auth API under /auth/ so clients need exactly ONE
	// backend URL. Point AUTH_PROXY_URL at GoTrue — the compose stack's
	// http://auth:9999, or hosted Supabase's https://<ref>.supabase.co/auth/v1
	// (set AUTH_PROXY_APIKEY to the project's anon key for the latter).
	if authURL := os.Getenv("AUTH_PROXY_URL"); authURL != "" {
		target, err := url.Parse(authURL)
		if err != nil {
			log.Error("AUTH_PROXY_URL", "err", err)
			os.Exit(1)
		}
		proxy := httputil.NewSingleHostReverseProxy(target)
		apikey := os.Getenv("AUTH_PROXY_APIKEY")
		baseDirector := proxy.Director
		proxy.Director = func(r *http.Request) {
			baseDirector(r)
			// Hosted Supabase sits behind a CDN that routes on Host and
			// rejects requests without the project's api key.
			r.Host = target.Host
			if apikey != "" {
				r.Header.Set("apikey", apikey)
				if r.Header.Get("Authorization") == "" {
					r.Header.Set("Authorization", "Bearer "+apikey)
				}
			}
		}
		proxy.ModifyResponse = func(resp *http.Response) error {
			// Our CORS middleware already sets these; doubled headers make
			// browsers reject the response.
			resp.Header.Del("Access-Control-Allow-Origin")
			resp.Header.Del("Access-Control-Allow-Headers")
			resp.Header.Del("Access-Control-Allow-Methods")
			return nil
		}
		mux.Handle("/auth/", http.StripPrefix("/auth", proxy))
		log.Info("auth proxy", "target", authURL)
	}
	mux.HandleFunc("GET /sync/{board}", func(w http.ResponseWriter, r *http.Request) {
		boardID := r.PathValue("board")
		userID, err := verifier.UserID(r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !openBoards {
			ok, err := st.CanAccess(r.Context(), boardID, userID)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if !ok {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			// Browser cross-origin websockets are wanted here; auth is the
			// JWT, not the Origin header.
			OriginPatterns: []string{"*"},
		})
		if err != nil {
			return
		}
		defer ws.CloseNow()
		log.Info("sync connected", "board", boardID, "user", userID)
		h.Serve(r.Context(), ws, boardID, userID)
		log.Info("sync disconnected", "board", boardID, "user", userID)
	})

	log.Info("listening", "addr", addr)
	if err := http.ListenAndServe(addr, cors(mux)); err != nil {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}

func cors(next http.Handler) http.Handler {
	origin := envOr("CORS_ORIGIN", "*")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		// Reflect whatever the browser asks for — auth clients send extra
		// headers (x-client-info, apikey) beyond the obvious two.
		if req := r.Header.Get("Access-Control-Request-Headers"); req != "" {
			w.Header().Set("Access-Control-Allow-Headers", req)
		} else {
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
