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
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
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
