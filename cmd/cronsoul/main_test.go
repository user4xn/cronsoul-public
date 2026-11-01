package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/user4xn/cronsoul/internal/auth"
	"github.com/user4xn/cronsoul/internal/store"
)

func TestMainPlayRouteIsReachableUnderJobs(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.Migrate(); err != nil {
		t.Fatal(err)
	}
	password, err := store.HashPassword("strong-password")
	if err != nil {
		t.Fatal(err)
	}
	uid, token := store.ID(), hex.EncodeToString([]byte("01234567890123456789012345678901"))
	if _, err = st.DB.Exec("INSERT INTO users(id,username,password_hash,role,must_change_password) VALUES(?,?,?,?,0)", uid, "qa", password, "qa"); err != nil {
		t.Fatal(err)
	}
	raw, _ := hex.DecodeString(token)
	sum := sha256.Sum256(raw)
	if _, err = st.DB.Exec("INSERT INTO sessions(id,user_id,token_hash,expires_at) VALUES(?,?,?,?)", store.ID(), uid, hex.EncodeToString(sum[:]), time.Now().Add(time.Hour).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	router := newRouter(&auth.Service{DB: st.DB, TTL: time.Hour}, st.DB, "")
	jobID := store.ID()
	if _, err = st.DB.Exec(`INSERT INTO jobs(id,key,name,service,method,url,cron) VALUES(?,?,?,?,?,?,?)`, jobID, "play-route", "Play", "svc", "GET", "https://example.test/x", "0 * * * *"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+jobID+"/run", bytes.NewBufferString(`{"use_assertions":false}`))
	req.AddCookie(&http.Cookie{Name: "cs_session", Value: token})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("play route status=%d body=%s", rec.Code, rec.Body)
	}
	var queued struct {
		RunID  string `json:"run_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &queued); err != nil || queued.RunID == "" {
		t.Fatalf("play response: %v %s", err, rec.Body)
	}
	var override *int
	if err := st.DB.QueryRow("SELECT assertions_override FROM runs WHERE id=?", queued.RunID).Scan(&override); err != nil {
		t.Fatal(err)
	}
	if override == nil || *override != 0 {
		t.Fatalf("per-run override not persisted: %v", override)
	}
	// runs history stays reachable through the parent mount
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/runs", nil)
	listReq.AddCookie(&http.Cookie{Name: "cs_session", Value: token})
	listed := httptest.NewRecorder()
	router.ServeHTTP(listed, listReq)
	if listed.Code != http.StatusOK {
		t.Fatalf("runs route status=%d body=%s", listed.Code, listed.Body)
	}
	var page struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &page); err != nil || len(page.Data) != 1 {
		t.Fatalf("runs page: %v %s", err, listed.Body)
	}
}

func TestMainPlayRequiresQA(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.Migrate(); err != nil {
		t.Fatal(err)
	}
	password, err := store.HashPassword("strong-password")
	if err != nil {
		t.Fatal(err)
	}
	uid, token := store.ID(), hex.EncodeToString([]byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	if _, err = st.DB.Exec("INSERT INTO users(id,username,password_hash,role,must_change_password) VALUES(?,?,?,?,0)", uid, "viewer", password, "viewer"); err != nil {
		t.Fatal(err)
	}
	raw, _ := hex.DecodeString(token)
	sum := sha256.Sum256(raw)
	if _, err = st.DB.Exec("INSERT INTO sessions(id,user_id,token_hash,expires_at) VALUES(?,?,?,?)", store.ID(), uid, hex.EncodeToString(sum[:]), time.Now().Add(time.Hour).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	router := newRouter(&auth.Service{DB: st.DB, TTL: time.Hour}, st.DB, "")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/whatever/run", bytes.NewBufferString(""))
	req.AddCookie(&http.Cookie{Name: "cs_session", Value: token})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer play status=%d body=%s", rec.Code, rec.Body)
	}
}

func TestMainRouterJobsDocumentedPaths(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.Migrate(); err != nil {
		t.Fatal(err)
	}
	password, err := store.HashPassword("strong-password")
	if err != nil {
		t.Fatal(err)
	}
	uid, token := store.ID(), hex.EncodeToString([]byte("01234567890123456789012345678901"))
	if _, err = st.DB.Exec("INSERT INTO users(id,username,password_hash,role,must_change_password) VALUES(?,?,?,?,0)", uid, "admin", password, "admin"); err != nil {
		t.Fatal(err)
	}
	raw, _ := hex.DecodeString(token)
	sum := sha256.Sum256(raw)
	if _, err = st.DB.Exec("INSERT INTO sessions(id,user_id,token_hash,expires_at) VALUES(?,?,?,?)", store.ID(), uid, hex.EncodeToString(sum[:]), time.Now().Add(time.Hour).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	router := newRouter(&auth.Service{DB: st.DB, TTL: time.Hour}, st.DB, "")
	unauthenticated := httptest.NewRecorder()
	router.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET status=%d", unauthenticated.Code)
	}
	post := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewBufferString(`{"key":"main-route","name":"Main route","service":"svc","method":"GET","url":"https://example.test","cron":"0 1 * * *"}`))
	post.AddCookie(&http.Cookie{Name: "cs_session", Value: token})
	created := httptest.NewRecorder()
	router.ServeHTTP(created, post)
	if created.Code != http.StatusCreated {
		t.Fatalf("documented POST status=%d body=%s", created.Code, created.Body)
	}
	get := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	get.AddCookie(&http.Cookie{Name: "cs_session", Value: token})
	listed := httptest.NewRecorder()
	router.ServeHTTP(listed, get)
	if listed.Code != http.StatusOK {
		t.Fatalf("documented GET status=%d body=%s", listed.Code, listed.Body)
	}
}
