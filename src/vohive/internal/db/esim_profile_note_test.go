package db

import (
	"path/filepath"
	"testing"
)

func TestEsimProfileNoteCRUD(t *testing.T) {
	if err := Init(filepath.Join(t.TempDir(), "esim-notes.db")); err != nil {
		t.Fatalf("Init() error=%v", err)
	}
	t.Cleanup(func() { DB = nil })

	if got, err := GetEsimProfileNote("iccid-a"); err != nil || got != "" {
		t.Fatalf("empty note = %q, error=%v", got, err)
	}
	if err := UpsertEsimProfileNote(" iccid-a ", "  Vodafone NL  "); err != nil {
		t.Fatalf("UpsertEsimProfileNote() error=%v", err)
	}
	if err := UpsertEsimProfileNote("iccid-b", "China Mobile"); err != nil {
		t.Fatalf("UpsertEsimProfileNote(second) error=%v", err)
	}

	got, err := GetEsimProfileNote("iccid-a")
	if err != nil || got != "Vodafone NL" {
		t.Fatalf("saved note = %q, error=%v", got, err)
	}
	notes, err := GetEsimProfileNotes([]string{"iccid-a", "iccid-b", "missing"})
	if err != nil || notes["iccid-a"] != "Vodafone NL" || notes["iccid-b"] != "China Mobile" {
		t.Fatalf("notes = %#v, error=%v", notes, err)
	}

	if err := UpsertEsimProfileExpiry("iccid-a", "2026-08-15"); err != nil {
		t.Fatalf("UpsertEsimProfileExpiry() error=%v", err)
	}
	expiries, err := GetEsimProfileExpiryDates([]string{"iccid-a", "missing"})
	if err != nil || expiries["iccid-a"] != "2026-08-15" {
		t.Fatalf("expiries = %#v, error=%v", expiries, err)
	}

	if err := UpsertEsimProfileNote("iccid-a", " "); err != nil {
		t.Fatalf("clear note error=%v", err)
	}
	if got, err := GetEsimProfileNote("iccid-a"); err != nil || got != "" {
		t.Fatalf("cleared note = %q, error=%v", got, err)
	}
	if expiries, err := GetEsimProfileExpiryDates([]string{"iccid-a"}); err != nil || expiries["iccid-a"] != "2026-08-15" {
		t.Fatalf("expiry should survive note clear, expiries=%#v error=%v", expiries, err)
	}
	if err := UpsertEsimProfileExpiry("iccid-a", " "); err != nil {
		t.Fatalf("clear expiry error=%v", err)
	}
	if expiries, err := GetEsimProfileExpiryDates([]string{"iccid-a"}); err != nil || len(expiries) != 0 {
		t.Fatalf("cleared expiry = %#v, error=%v", expiries, err)
	}
	if _, err := NormalizeEsimProfileExpiryDate("2026/08/15"); err == nil {
		t.Fatal("invalid expiry date should return an error")
	}
}
