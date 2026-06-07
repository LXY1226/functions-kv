package functionskv

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type testValue struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	RefreshTime  int64  `json:"refresh_time"`
}

func (v testValue) valid() bool {
	return v.AccessToken != "" && v.RefreshToken != ""
}

func TestNormalizeAuthCookie(t *testing.T) {
	if got := NormalizeAuthCookie("secret"); got != "__Host-Auth=secret" {
		t.Fatalf("NormalizeAuthCookie token = %q", got)
	}
	if got := NormalizeAuthCookie("__Host-Auth=secret"); got != "__Host-Auth=secret" {
		t.Fatalf("NormalizeAuthCookie cookie = %q", got)
	}
}

func TestInitSavesLocalWhenRemoteMissing(t *testing.T) {
	server := newKVTestServer(t)
	defer server.Close()

	client := New[testValue](server.URL, "secret", "token",
		WithValidator[testValue](func(v testValue) bool { return v.valid() }),
		WithBeforeSave[testValue](func(v testValue) testValue {
			v.RefreshTime = 42
			return v
		}),
	)
	value, err := client.Init(context.Background(), testValue{AccessToken: "a", RefreshToken: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if value.AccessToken != "a" || value.RefreshToken != "r" {
		t.Fatalf("Init value = %#v", value)
	}

	remote, err := client.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if remote.Value.RefreshTime != 42 {
		t.Fatalf("remote RefreshTime = %d", remote.Value.RefreshTime)
	}
}

func TestBeforeRefreshReturnsRemoteWhenVersionChanged(t *testing.T) {
	server := newKVTestServer(t)
	defer server.Close()

	client := New[testValue](server.URL, "secret", "token",
		WithValidator[testValue](func(v testValue) bool { return v.valid() }),
	)
	if err := client.Save(context.Background(), testValue{AccessToken: "old", RefreshToken: "old-r"}); err != nil {
		t.Fatal(err)
	}
	other := New[testValue](server.URL, "secret", "token",
		WithValidator[testValue](func(v testValue) bool { return v.valid() }),
	)
	if err := other.Save(context.Background(), testValue{AccessToken: "new", RefreshToken: "new-r"}); err != nil {
		t.Fatal(err)
	}

	value, err := client.BeforeRefresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if value == nil || value.AccessToken != "new" {
		t.Fatalf("BeforeRefresh value = %#v", value)
	}
}

func TestBeforeAndAfterRefreshLockLifecycle(t *testing.T) {
	server := newKVTestServer(t)
	defer server.Close()

	client := New[testValue](server.URL, "secret", "token",
		WithValidator[testValue](func(v testValue) bool { return v.valid() }),
	)
	if err := client.Save(context.Background(), testValue{AccessToken: "old", RefreshToken: "old-r"}); err != nil {
		t.Fatal(err)
	}

	value, err := client.BeforeRefresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if value != nil {
		t.Fatalf("BeforeRefresh while lock acquired = %#v", value)
	}
	if err := client.AfterRefresh(context.Background(), testValue{AccessToken: "new", RefreshToken: "new-r"}); err != nil {
		t.Fatal(err)
	}
	remote, err := client.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if remote.Value.AccessToken != "new" {
		t.Fatalf("remote token = %#v", remote.Value)
	}
}

func newKVTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	var value string
	var version string
	var locked bool
	nextVersion := func() string {
		version += "x"
		return version
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Cookie"), "__Host-Auth=secret") {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if r.URL.Path != "/token" {
			http.Error(w, "Bad key", http.StatusBadRequest)
			return
		}
		switch r.Method {
		case http.MethodGet:
			if value == "" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set(VersionHeader, version)
			_, _ = w.Write([]byte(value))
		case http.MethodPost:
			body, _ := ioReadAllString(r)
			if !validJSON(t, body) {
				http.Error(w, "Bad value", http.StatusBadRequest)
				return
			}
			value = body
			nextVersion()
			w.Header().Set(VersionHeader, version)
			w.WriteHeader(http.StatusNoContent)
		case "LOCK":
			expect := r.URL.Query().Get("t")
			if value == "" {
				http.NotFound(w, r)
				return
			}
			if expect != version {
				w.Header().Set(VersionHeader, version)
				_, _ = w.Write([]byte(value))
				return
			}
			if locked {
				http.Error(w, "Locked", http.StatusLocked)
				return
			}
			locked = true
			w.WriteHeader(http.StatusCreated)
		case "UNLOCK":
			body, _ := ioReadAllString(r)
			if !validJSON(t, body) {
				http.Error(w, "Bad value", http.StatusBadRequest)
				return
			}
			value = body
			nextVersion()
			locked = false
			w.Header().Set(VersionHeader, version)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}))
}

func validJSON(t *testing.T, body string) bool {
	t.Helper()
	var value testValue
	return json.Unmarshal([]byte(body), &value) == nil && value.valid()
}

func ioReadAllString(r *http.Request) (string, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
