package ingest

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/analytics"
)

var (
	eventsPerInstallPerMinute = window{limit: "events_per_install_per_minute", value: 10_000, span: time.Minute, bucket: time.Second}
	eventsPerInstallPerDay    = window{limit: "events_per_install_per_day", value: 1_000_000, span: 24 * time.Hour, bucket: time.Minute}
	installsPerIPPerHour      = window{limit: "installs_per_ip_per_hour", value: 20, span: time.Hour, bucket: time.Minute}
)

const (
	minIPSecretBytes = 32
	batchesInFlight  = 2
)

type Config struct {
	ClickHouseURL  string
	IPSecret       []byte
	ClientIPHeader string
	Logf           func(format string, args ...any)
}

type Server struct {
	db             *clickHouse
	ipSecret       []byte
	clientIPHeader string
	events         *quota
	registrations  *quota
	installs       keyedMutex
	batchSlots     chan struct{}
	metrics        *metrics
	logf           func(format string, args ...any)
}

func New(ctx context.Context, cfg Config) (*Server, error) {
	if len(cfg.IPSecret) < minIPSecretBytes {
		return nil, fmt.Errorf("the IP hashing secret must be at least %d bytes, got %d", minIPSecretBytes, len(cfg.IPSecret))
	}
	db, err := newClickHouse(cfg.ClickHouseURL)
	if err != nil {
		return nil, err
	}
	if err := db.applySchema(ctx); err != nil {
		return nil, err
	}
	return &Server{
		db:             db,
		ipSecret:       cfg.IPSecret,
		clientIPHeader: cfg.ClientIPHeader,
		events:         newQuota(eventsPerInstallPerMinute, eventsPerInstallPerDay),
		registrations:  newQuota(installsPerIPPerHour),
		installs:       keyedMutex{held: map[string]*keyLock{}},
		batchSlots:     make(chan struct{}, batchesInFlight),
		metrics:        newMetrics(),
		logf:           cfg.Logf,
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST /v1/installs", s.route("register", s.register))
	mux.Handle("POST /v1/events", s.route("events", s.acceptEvents))
	mux.Handle("GET /v1/installs/me", s.route("installs_me_get", s.holdings))
	mux.Handle("DELETE /v1/installs/me", s.route("installs_me_delete", s.deleteInstall))
	mux.HandleFunc("GET /metrics", s.metrics.serve)
	mux.HandleFunc("GET /healthz", s.healthz)
	return mux
}

type handlerFunc func(w http.ResponseWriter, r *http.Request) error

type install struct {
	ID        string    `json:"install_id"`
	CreatedAt time.Time `json:"created_at"`
}

type unauthorizedError struct{}

func (unauthorizedError) Error() string {
	return "send the install token from POST /v1/installs as Authorization: Bearer <token>"
}

type quarantinedError struct{ reason string }

func (e *quarantinedError) Error() string { return "this install is quarantined: " + e.reason }

func (s *Server) route(name string, handle handlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := http.StatusOK
		if err := handle(w, r); err != nil {
			status = s.writeError(w, name, err)
		}
		s.metrics.request(name, status)
	})
}

func (s *Server) writeError(w http.ResponseWriter, route string, err error) int {
	var refused *refusedError
	var bad *malformedError
	var quarantined *quarantinedError
	switch {
	case errors.As(err, &refused):
		s.metrics.refusal(refused.refusal.Limit)
		if refused.refusal.RetryAfterS > 0 {
			w.Header().Set("Retry-After", strconv.FormatInt(refused.refusal.RetryAfterS, 10))
		}
		writeJSON(w, refused.status, refused.refusal)
		return refused.status
	case errors.As(err, &bad):
		writeJSON(w, http.StatusBadRequest, errorBody{Error: bad.message})
		return http.StatusBadRequest
	case errors.As(err, &unauthorizedError{}):
		writeJSON(w, http.StatusUnauthorized, errorBody{Error: err.Error()})
		return http.StatusUnauthorized
	case errors.As(err, &quarantined):
		writeJSON(w, http.StatusForbidden, errorBody{Error: quarantined.Error()})
		return http.StatusForbidden
	default:
		s.logf("attn-ingest: %s: %v", route, err)
		writeJSON(w, http.StatusServiceUnavailable, errorBody{Error: "the ingest could not store this request; retry later"})
		return http.StatusServiceUnavailable
	}
}

type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) error {
	now := time.Now().UTC()
	ipHash := s.ipHash(r)
	if err := s.checkQuarantine(r.Context(), "", ipHash); err != nil {
		return err
	}
	if refusal := s.registrations.admit(ipHash, 1, now); refusal != nil {
		return &refusedError{status: http.StatusTooManyRequests, refusal: *refusal}
	}
	registration := analytics.Registration{InstallID: "i-" + randomHex(16), Token: randomToken()}
	row := installRow{InstallID: registration.InstallID, TokenSHA256: tokenHash(registration.Token), IPHash: ipHash, CreatedAt: now}
	if err := insert(r.Context(), s.db, "installs", []installRow{row}); err != nil {
		return err
	}
	s.metrics.registered.Add(1)
	writeJSON(w, http.StatusOK, registration)
	return nil
}

type installRow struct {
	InstallID   string    `json:"install_id"`
	TokenSHA256 string    `json:"token_sha256"`
	IPHash      string    `json:"ip_hash"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Server) acceptEvents(w http.ResponseWriter, r *http.Request) error {
	inst, release, err := s.holdInstall(r)
	if err != nil {
		return err
	}
	defer release()
	ipHash := s.ipHash(r)
	if err := s.checkQuarantine(r.Context(), inst.ID, ipHash); err != nil {
		return err
	}
	select {
	case s.batchSlots <- struct{}{}:
		defer func() { <-s.batchSlots }()
	case <-r.Context().Done():
		return r.Context().Err()
	}
	now := time.Now().UTC()
	batch, gzipBytes, err := readBatch(r.Body, r.ContentLength)
	if err != nil {
		return err
	}
	if refusal := s.events.admit(inst.ID, int64(len(batch.Records)), now); refusal != nil {
		return &refusedError{status: http.StatusTooManyRequests, refusal: *refusal}
	}
	fresh, err := s.withoutStored(r.Context(), inst.ID, batch.Records)
	if err != nil {
		return err
	}
	if len(fresh) > 0 {
		rows := make([]eventRow, len(fresh))
		for i, record := range fresh {
			rows[i] = newEventRow(inst.ID, ipHash, now, batch.Build, record)
		}
		if err := insert(context.WithoutCancel(r.Context()), s.db, "events", rows); err != nil {
			return err
		}
	}
	accepted := analytics.Accepted{Accepted: len(fresh), Duplicates: len(batch.Records) - len(fresh)}
	s.metrics.accepted.Add(int64(accepted.Accepted))
	s.metrics.duplicates.Add(int64(accepted.Duplicates))
	s.metrics.acceptedBytes.Add(gzipBytes)
	writeJSON(w, http.StatusOK, accepted)
	return nil
}

type streamSeq struct {
	Stream analytics.Stream `json:"stream"`
	Seq    int64            `json:"seq"`
}

func (s *Server) withoutStored(ctx context.Context, installID string, records []analytics.Record) ([]analytics.Record, error) {
	seqs := map[analytics.Stream][]string{}
	for _, record := range records {
		seqs[record.Stream] = append(seqs[record.Stream], strconv.FormatInt(record.Seq, 10))
	}
	seen := map[streamSeq]bool{}
	for stream, submitted := range seqs {
		err := s.db.rows(ctx,
			"SELECT stream, seq FROM events WHERE install_id = {install:String} AND stream = {stream:String} AND seq IN {seqs:Array(Int64)}",
			map[string]string{"install": installID, "stream": string(stream), "seqs": "[" + strings.Join(submitted, ",") + "]"},
			func(row json.RawMessage) error {
				var key streamSeq
				if err := json.Unmarshal(row, &key); err != nil {
					return err
				}
				seen[key] = true
				return nil
			})
		if err != nil {
			return nil, err
		}
	}
	fresh := records[:0:0]
	for _, record := range records {
		key := streamSeq{Stream: record.Stream, Seq: record.Seq}
		if seen[key] {
			continue
		}
		seen[key] = true
		fresh = append(fresh, record)
	}
	return fresh, nil
}

type eventRow struct {
	InstallID        string           `json:"install_id"`
	Stream           analytics.Stream `json:"stream"`
	Seq              int64            `json:"seq"`
	Name             string           `json:"name"`
	Subject          string           `json:"subject"`
	At               time.Time        `json:"at"`
	Bytes            int              `json:"bytes"`
	Props            string           `json:"props"`
	BuildVersion     string           `json:"build_version"`
	BuildCommit      string           `json:"build_commit"`
	BuildChannel     string           `json:"build_channel"`
	BuildTime        string           `json:"build_time"`
	BuildFingerprint string           `json:"build_fingerprint"`
	BuildProtocol    string           `json:"build_protocol"`
	BuildOS          string           `json:"build_os"`
	BuildArch        string           `json:"build_arch"`
	BuildDirty       bool             `json:"build_dirty"`
	AppVersion       string           `json:"app_version"`
	AppChannel       string           `json:"app_channel"`
	IPHash           string           `json:"ip_hash"`
	ReceivedAt       time.Time        `json:"received_at"`
}

func newEventRow(installID, ipHash string, receivedAt time.Time, build analytics.Build, record analytics.Record) eventRow {
	return eventRow{
		InstallID:        installID,
		Stream:           record.Stream,
		Seq:              record.Seq,
		Name:             record.Name,
		Subject:          record.Subject,
		At:               record.At.UTC(),
		Bytes:            record.Bytes,
		Props:            string(record.Props),
		BuildVersion:     build.Version,
		BuildCommit:      build.Commit,
		BuildChannel:     build.Channel,
		BuildTime:        build.BuildTime,
		BuildFingerprint: build.Fingerprint,
		BuildProtocol:    build.Protocol,
		BuildOS:          build.Os,
		BuildArch:        build.Arch,
		BuildDirty:       build.Dirty,
		AppVersion:       build.AppVersion,
		AppChannel:       build.AppChannel,
		IPHash:           ipHash,
		ReceivedAt:       receivedAt,
	}
}

func (s *Server) holdings(w http.ResponseWriter, r *http.Request) error {
	token, err := bearerTokenHash(r)
	if err != nil {
		return err
	}
	inst, err := s.authenticate(r.Context(), token)
	if err != nil {
		return err
	}
	holdings := analytics.InstallHoldings{InstallID: inst.ID, CreatedAt: inst.CreatedAt, Streams: map[analytics.Stream]int64{}}
	err = s.db.rows(r.Context(),
		"SELECT stream, count() AS events, min(at) AS first_at, max(at) AS last_at FROM events WHERE install_id = {install:String} GROUP BY stream",
		map[string]string{"install": inst.ID},
		func(row json.RawMessage) error {
			var stream struct {
				Stream  analytics.Stream `json:"stream"`
				Events  int64            `json:"events"`
				FirstAt time.Time        `json:"first_at"`
				LastAt  time.Time        `json:"last_at"`
			}
			if err := json.Unmarshal(row, &stream); err != nil {
				return err
			}
			holdings.Streams[stream.Stream] = stream.Events
			holdings.Events += stream.Events
			if holdings.FirstAt == nil || stream.FirstAt.Before(*holdings.FirstAt) {
				holdings.FirstAt = &stream.FirstAt
			}
			if holdings.LastAt == nil || stream.LastAt.After(*holdings.LastAt) {
				holdings.LastAt = &stream.LastAt
			}
			return nil
		})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, holdings)
	return nil
}

func (s *Server) deleteInstall(w http.ResponseWriter, r *http.Request) error {
	inst, release, err := s.holdInstall(r)
	if err != nil {
		return err
	}
	defer release()
	params := map[string]string{"install": inst.ID}
	settled := context.WithoutCancel(r.Context())
	if err := s.db.exec(settled, "DELETE FROM events WHERE install_id = {install:String}", params); err != nil {
		return err
	}
	if err := s.db.exec(settled, "DELETE FROM installs WHERE install_id = {install:String}", params); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if err := s.db.exec(r.Context(), "SELECT 1", nil); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, errorBody{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) holdInstall(r *http.Request) (install, func(), error) {
	token, err := bearerTokenHash(r)
	if err != nil {
		return install{}, nil, err
	}
	release := s.installs.lock(token)
	inst, err := s.authenticate(r.Context(), token)
	if err != nil {
		release()
		return install{}, nil, err
	}
	return inst, release, nil
}

func bearerTokenHash(r *http.Request) (string, error) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		return "", unauthorizedError{}
	}
	return tokenHash(token), nil
}

func (s *Server) authenticate(ctx context.Context, tokenSHA256 string) (install, error) {
	var found *install
	err := s.db.rows(ctx,
		"SELECT install_id, created_at FROM installs WHERE token_sha256 = {token:String} LIMIT 1",
		map[string]string{"token": tokenSHA256},
		func(row json.RawMessage) error {
			found = &install{}
			return json.Unmarshal(row, found)
		})
	if err != nil {
		return install{}, err
	}
	if found == nil {
		return install{}, unauthorizedError{}
	}
	return *found, nil
}

func (s *Server) checkQuarantine(ctx context.Context, installID, ipHash string) error {
	var reason *string
	err := s.db.rows(ctx,
		"SELECT reason FROM quarantine WHERE (install_id != '' AND install_id = {install:String}) OR (ip_hash != '' AND ip_hash = {ip:String}) LIMIT 1",
		map[string]string{"install": installID, "ip": ipHash},
		func(row json.RawMessage) error {
			var entry struct {
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(row, &entry); err != nil {
				return err
			}
			reason = &entry.Reason
			return nil
		})
	if err != nil {
		return err
	}
	if reason != nil {
		return &quarantinedError{reason: *reason}
	}
	return nil
}

func (s *Server) ipHash(r *http.Request) string {
	mac := hmac.New(sha256.New, s.ipSecret)
	mac.Write([]byte(s.clientIP(r)))
	return hex.EncodeToString(mac.Sum(nil)[:16])
}

func (s *Server) clientIP(r *http.Request) string {
	if s.clientIPHeader != "" {
		if value := r.Header.Get(s.clientIPHeader); value != "" {
			first, _, _ := strings.Cut(value, ",")
			return strings.TrimSpace(first)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
