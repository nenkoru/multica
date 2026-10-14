package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/auth"
)

func TestAuth_KioskCredentialsAreForbidden(t *testing.T) {
	cases := []struct {
		name  string
		setup func(r *http.Request)
	}{
		{"bearer kiosk token", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+auth.KioskTokenPrefix+"0123456789abcdef")
		}},
		{"kiosk cookie alone", func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: auth.KioskCookieName, Value: auth.KioskTokenPrefix + "0123456789abcdef"})
		}},
	}
	for _, tc := range cases {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			t.Run(tc.name+" "+method, func(t *testing.T) {
				handler := authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					t.Fatal("next handler should not be called")
				}))
				req := httptest.NewRequest(method, "/api/issues/RUSL-1/comments", nil)
				tc.setup(req)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
				if w.Code != http.StatusForbidden {
					t.Fatalf("expected 403, got %d", w.Code)
				}
			})
		}
	}
}

func TestAuth_KioskCookieDoesNotShadowSession(t *testing.T) {
	handler := authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+generateToken(validClaims(), auth.JWTSecret()))
	req.AddCookie(&http.Cookie{Name: auth.KioskCookieName, Value: auth.KioskTokenPrefix + "0123456789abcdef"})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected the session to authenticate, got %d", w.Code)
	}
}
