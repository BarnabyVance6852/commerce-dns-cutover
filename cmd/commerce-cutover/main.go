package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"example.com/commerce-dns-cutover/cutover"
	"example.com/commerce-dns-cutover/infrai"
)

type dnsClient interface {
	ZoneID(context.Context, string) (string, error)
	Records(context.Context, string) ([]cutover.Record, error)
	UpsertRecord(context.Context, string, cutover.Record) error
}

type service struct {
	dns dnsClient
}

type cutoverResult struct {
	Status   string           `json:"status"`
	Applied  []cutover.Record `json:"applied"`
	Rollback cutover.Rollback `json:"rollback"`
}

type rollbackResult struct {
	Status   string           `json:"status"`
	Restored []cutover.Record `json:"restored"`
}

func main() {
	apiKey := os.Getenv("INFRAI_API_KEY")
	if apiKey == "" {
		log.Fatal("INFRAI_API_KEY is required")
	}
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	svc := &service{dns: infrai.NewClient(apiKey)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /cutovers", svc.handleCutover)
	mux.HandleFunc("POST /rollbacks", svc.handleRollback)
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("commerce cutover service listening on %s", addr)
	log.Fatal(server.ListenAndServe())
}

func (s *service) handleCutover(w http.ResponseWriter, r *http.Request) {
	var input cutover.Request
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	records, err := cutover.BuildPlan(input)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}

	zoneID, err := s.dns.ZoneID(r.Context(), input.Domain)
	if err != nil {
		writeClientError(w, err)
		return
	}
	current, err := s.dns.Records(r.Context(), zoneID)
	if err != nil {
		writeClientError(w, err)
		return
	}
	previous := recordsForNames(current, records)
	for _, record := range records {
		if err := s.dns.UpsertRecord(r.Context(), zoneID, record); err != nil {
			writeClientError(w, err)
			return
		}
	}

	writeJSON(w, http.StatusOK, cutoverResult{
		Status:   "cutover_applied",
		Applied:  records,
		Rollback: cutover.Rollback{Domain: input.Domain, ZoneID: zoneID, PreviousRecords: previous},
	})
}

func (s *service) handleRollback(w http.ResponseWriter, r *http.Request) {
	var input cutover.Rollback
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if input.Domain == "" || input.ZoneID == "" || len(input.PreviousRecords) == 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "domain, zone_id, and previous_records are required"})
		return
	}
	zoneID, err := s.dns.ZoneID(r.Context(), input.Domain)
	if err != nil {
		writeClientError(w, err)
		return
	}
	if zoneID != input.ZoneID {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "zone_id no longer matches domain"})
		return
	}
	for _, record := range input.PreviousRecords {
		if err := s.dns.UpsertRecord(r.Context(), zoneID, record); err != nil {
			writeClientError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, rollbackResult{Status: "rollback_applied", Restored: input.PreviousRecords})
}

func recordsForNames(current, planned []cutover.Record) []cutover.Record {
	wanted := make(map[string]bool, len(planned))
	for _, record := range planned {
		wanted[record.Name] = true
	}
	var previous []cutover.Record
	for _, record := range current {
		if wanted[record.Name] && record.RecordType == "CNAME" {
			previous = append(previous, record)
		}
	}
	return previous
}

func decodeJSON(r *http.Request, target any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	return nil
}

func writeClientError(w http.ResponseWriter, err error) {
	var apiErr *infrai.APIError
	if errors.As(err, &apiErr) && apiErr.HTTPStatus >= 400 && apiErr.HTTPStatus < 500 {
		writeJSON(w, apiErr.HTTPStatus, map[string]string{"error": apiErr.Message, "code": apiErr.Code})
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("encode response: %v", err)
	}
}
