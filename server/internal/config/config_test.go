package config

import (
	"reflect"
	"testing"
)

func TestLoadRegistrationAndAdminEnvironment(t *testing.T) {
	t.Setenv("OPEN_REGISTRATION", "true")
	t.Setenv("OPENID_WHITELIST", " allowed, another ")
	t.Setenv("ADMIN_OPENIDS", " owner, friend ,, ")

	cfg := Load()
	if !cfg.OpenRegistration {
		t.Fatal("OPEN_REGISTRATION was not loaded")
	}
	if want := []string{"allowed", "another"}; !reflect.DeepEqual(cfg.OpenIDWhitelist, want) {
		t.Fatalf("OPENID_WHITELIST = %v, want %v", cfg.OpenIDWhitelist, want)
	}
	if want := []string{"owner", "friend"}; !reflect.DeepEqual(cfg.AdminOpenIDs, want) {
		t.Fatalf("ADMIN_OPENIDS = %v, want %v", cfg.AdminOpenIDs, want)
	}
}
