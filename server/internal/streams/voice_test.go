package streams

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakeWhisper(t *testing.T, reply string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != wantAuth {
			t.Errorf("auth %q, want %q", got, wantAuth)
		}
		if r.URL.Path != "/v1/audio/transcriptions" {
			t.Errorf("path %s", r.URL.Path)
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			t.Errorf("no file: %v", err)
		} else {
			b, _ := io.ReadAll(f)
			if string(b) != "OPUS" {
				t.Errorf("audio %q", b)
			}
		}
		if r.FormValue("language") != "ru" || r.FormValue("model") != "whisper-1" {
			t.Errorf("form %v", r.Form)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
}

// wantAuth is the Authorization header fakeWhisper expects.
var wantAuth string

func TestWhisperClient(t *testing.T) {
	wantAuth = ""
	if NewWhisperClient(" ", "", "") != nil {
		t.Fatal("empty url must keep voice off")
	}
	if c := NewWhisperClient("http://w.svc/v1/audio/transcriptions/", "", ""); c.BaseURL != "http://w.svc" {
		t.Fatalf("endpoint url kept as %q", c.BaseURL)
	}
	srv := fakeWhisper(t, `{"text": " давай про Терн "}`, 200)
	defer srv.Close()
	got, err := NewWhisperClient(srv.URL+"/", "", "").Transcribe(context.Background(), "v.webm", strings.NewReader("OPUS"))
	if err != nil || got != "давай про Терн" {
		t.Fatalf("%q %v", got, err)
	}
	bad := fakeWhisper(t, `{"error": "busy"}`, 503)
	defer bad.Close()
	if _, err := NewWhisperClient(bad.URL, "", "").Transcribe(context.Background(), "v.webm", strings.NewReader("OPUS")); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("err %v", err)
	}
	wantAuth = "Bearer tok"
	authed := fakeWhisper(t, `{"text": "да"}`, 200)
	defer authed.Close()
	if got, err := NewWhisperClient(authed.URL, "", " tok\n").Transcribe(context.Background(), "v.webm", strings.NewReader("OPUS")); err != nil || got != "да" {
		t.Fatalf("with token: %q %v", got, err)
	}
	wantAuth = ""
	empty := fakeWhisper(t, `{"text": ""}`, 200)
	defer empty.Close()
	if _, err := NewWhisperClient(empty.URL, "", "").Transcribe(context.Background(), "v.webm", strings.NewReader("OPUS")); err == nil {
		t.Fatal("empty text accepted")
	}
}

type stubVoice struct{ text string }

func (s stubVoice) Transcribe(context.Context, string, io.Reader) (string, error) { return s.text, nil }

func TestMultikaVoiceHandlerOffAndOn(t *testing.T) {
	sup := &Supervisor{}
	h := &HTTP{Sup: sup, WorkspaceID: func(*http.Request) string { return "00000000-0000-0000-0000-000000000001" }}
	req := func(audio ...byte) *http.Request {
		if audio == nil {
			audio = []byte("OPUS")
		}
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, _ := mw.CreateFormFile("file", "v.webm")
		_, _ = fw.Write(audio)
		_ = mw.Close()
		r := httptest.NewRequest(http.MethodPost, "/api/streams/multika/voice", &body)
		r.Header.Set("Content-Type", mw.FormDataContentType())
		return r
	}
	rec := httptest.NewRecorder()
	h.MultikaVoice(rec, req())
	if rec.Code != http.StatusNotFound {
		t.Fatalf("off: %d %s", rec.Code, rec.Body)
	}
	_ = sup.MultikaAgentID.Scan("00000000-0000-0000-0000-000000000002")
	sup.Voice = stubVoice{text: "привет"}
	rec = httptest.NewRecorder()
	h.MultikaVoice(rec, req())
	var out map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusOK || out["text"] != "привет" {
		t.Fatalf("on: %d %s", rec.Code, rec.Body)
	}
	// Above the service's limit: refused here, never truncated on the way.
	rec = httptest.NewRecorder()
	h.MultikaVoice(rec, req(make([]byte, MaxVoiceBytes+1)...))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too large: %d %s", rec.Code, rec.Body)
	}
	// Far above it: MaxBytesReader cuts the body, still 413, not 400.
	rec = httptest.NewRecorder()
	h.MultikaVoice(rec, req(make([]byte, 2*MaxVoiceBytes)...))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("far too large: %d %s", rec.Code, rec.Body)
	}
}
