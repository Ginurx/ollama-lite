package config

import (
	"net/url"
	"testing"
)

func TestHostURL(t *testing.T) {
	cases := []struct {
		in, scheme, hostport string
	}{
		{"127.0.0.1:11435", "http", "127.0.0.1:11435"},
		{":11434", "http", ":11434"},
		{"0.0.0.0", "http", "0.0.0.0:11434"},
		{"0.0.0.0:11434", "http", "0.0.0.0:11434"},
		{"http://h:8080", "http", "h:8080"},
		{"https://h:443", "https", "h:443"},
		{"[::]:11434", "http", "[::]:11434"},
		{"::", "http", "[::]:11434"},
	}
	for _, c := range cases {
		u := hostURL(c.in)
		if u.Scheme != c.scheme || u.Host != c.hostport {
			t.Errorf("hostURL(%q) = %s://%s, want %s://%s", c.in, u.Scheme, u.Host, c.scheme, c.hostport)
		}
	}
}

func TestConnectableHost(t *testing.T) {
	cases := []struct {
		env, want string
	}{
		{"0.0.0.0:11434", "http://127.0.0.1:11434"},
		{":11434", "http://127.0.0.1:11434"},
		{"[::]:11434", "http://[::1]:11434"},
		{"127.0.0.1:11434", "http://127.0.0.1:11434"},
		{"192.168.1.10:11434", "http://192.168.1.10:11434"},
	}
	for _, c := range cases {
		t.Setenv("OLLAMA_HOST", c.env)
		if got := ConnectableHost().String(); got != c.want {
			t.Errorf("ConnectableHost() with OLLAMA_HOST=%q = %q, want %q", c.env, got, c.want)
		}
	}
}

func TestConnectableHostFrom(t *testing.T) {
	// Explicit override is a bind address → rewritten, original returned as bindOverride.
	rewriteCases := []struct {
		override, wantHost, wantBind string
	}{
		{"0.0.0.0:11434", "http://127.0.0.1:11434", "0.0.0.0:11434"},
		{":11434", "http://127.0.0.1:11434", ":11434"},
		{"[::]:11434", "http://[::1]:11434", "[::]:11434"},
	}
	for _, c := range rewriteCases {
		t.Run("rewrite/"+c.override, func(t *testing.T) {
			t.Setenv("OLLAMA_HOST", "127.0.0.1:11434") // must be ignored when override is set
			u, bind := ConnectableHostFrom(c.override)
			if u.String() != c.wantHost || bind != c.wantBind {
				t.Errorf("ConnectableHostFrom(%q) = (%q, %q), want (%q, %q)", c.override, u.String(), bind, c.wantHost, c.wantBind)
			}
		})
	}

	// Explicit override that's already connectable → unchanged, no bindOverride.
	t.Setenv("OLLAMA_HOST", "127.0.0.1:11434")
	u, bind := ConnectableHostFrom("127.0.0.1:11434")
	if u.String() != "http://127.0.0.1:11434" || bind != "" {
		t.Errorf("ConnectableHostFrom(127.0.0.1:11434) = (%q, %q), want (http://127.0.0.1:11434, \"\")", u.String(), bind)
	}

	// Empty override falls back to OLLAMA_HOST, silently (no bindOverride).
	t.Setenv("OLLAMA_HOST", "0.0.0.0:11434")
	u, bind = ConnectableHostFrom("")
	if u.String() != "http://127.0.0.1:11434" || bind != "" {
		t.Errorf("ConnectableHostFrom(\"\") = (%q, %q), want (http://127.0.0.1:11434, \"\")", u.String(), bind)
	}
}

// Ensure ConnectableHostFrom returns a usable *url.URL (non-nil) on all paths.
func TestConnectableHostFromNonNil(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "")
	if u, _ := ConnectableHostFrom(""); u == (*url.URL)(nil) {
		t.Fatal("ConnectableHostFrom(\"\") returned nil URL")
	}
}