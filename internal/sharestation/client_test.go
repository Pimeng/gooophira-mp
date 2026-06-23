package sharestation

import (
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUpload_ParsesScoreID(t *testing.T) {
	var gotChartName, gotUsername, gotAuth string
	var gotFile []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/upload_direct" || r.Method != http.MethodPost {
			http.Error(w, "bad", 400)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			data, _ := io.ReadAll(p)
			switch p.FormName() {
			case "file":
				gotFile = data
			case "chart_name":
				gotChartName = string(data)
			case "username":
				gotUsername = string(data)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"replay_id": "100_42_777.phirarec"})
	}))
	defer srv.Close()

	c := NewClient(Config{URL: srv.URL, Token: "secret"})
	res, err := c.Upload([]byte("REPLAYDATA"), "1.phirarec", "MyChart", "alice")
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if res.ScoreID != 777 {
		t.Errorf("scoreID = %d, want 777", res.ScoreID)
	}
	if res.ReplayID != "100_42_777.phirarec" {
		t.Errorf("replayID = %q", res.ReplayID)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if string(gotFile) != "REPLAYDATA" || gotChartName != "MyChart" || gotUsername != "alice" {
		t.Errorf("multipart fields wrong: file=%q chart=%q user=%q", gotFile, gotChartName, gotUsername)
	}
}

func TestUpload_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", 500)
	}))
	defer srv.Close()
	c := NewClient(Config{URL: srv.URL, Token: "x"})
	if _, err := c.Upload([]byte("d"), "1.phirarec", "", ""); err == nil {
		t.Error("expected error on 500")
	}
}

func TestSetVisibility(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c := NewClient(Config{URL: srv.URL, Token: "tok"})

	if err := c.SetVisibility(777, true); err != nil {
		t.Fatalf("show: %v", err)
	}
	if gotPath != "/show/777" {
		t.Errorf("show path = %q, want /show/777", gotPath)
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Errorf("missing auth header: %q", gotAuth)
	}

	if err := c.SetVisibility(777, false); err != nil {
		t.Fatalf("hide: %v", err)
	}
	if gotPath != "/hide/777" {
		t.Errorf("hide path = %q, want /hide/777", gotPath)
	}
}

func TestSetVisibility_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
	}))
	defer srv.Close()
	c := NewClient(Config{URL: srv.URL, Token: "x"})
	if err := c.SetVisibility(1, true); err == nil {
		t.Error("expected error on 403")
	}
}
