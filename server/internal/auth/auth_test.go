package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GeiQianJiuGan/sleepy-bookmark/server/internal/config"
)

func TestCode2SessionErrorHidesSecret(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close() // connection refused
	old := wxSessionURL
	wxSessionURL = srv.URL
	defer func() { wxSessionURL = old }()

	a := &Auth{cfg: config.Config{WXAppID: "wx-app", WXSecret: "top-secret-value"}}
	_, err := a.code2Session(context.Background(), "code")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "top-secret-value") || strings.Contains(err.Error(), "secret=") {
		t.Fatalf("error leaks the app secret: %v", err)
	}
}
